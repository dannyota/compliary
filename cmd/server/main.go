// Command server serves compliary's control-framework evidence over MCP
// (Streamable HTTP) for remote user-owned agents. It is the same evidence-only
// MCP surface as cmd/mcp (stdio), served over HTTP.
//
// Auth boundary (COMPLIARY_MCP_TOKEN):
//   - Token set + valid bearer → full projection (body, title_original, chunks).
//   - Token set + wrong/absent bearer → 401 Unauthorized.
//   - Token NOT set → reduced projection only (no body/title_original/content);
//     the server starts and logs WHY, never silently downgrading — an operator
//     who forgot the token sees the warning immediately.
//
// Endpoints: /mcp (MCP Streamable HTTP), /healthz (health check).
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"danny.vn/compliary/pkg/base/config"
	"danny.vn/compliary/pkg/base/db"
	clog "danny.vn/compliary/pkg/base/log"
	"danny.vn/compliary/pkg/mcp"
	"danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/embed/onnxembed"
	"danny.vn/compliary/pkg/rag/retrieve"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "config/config.yaml", "path to config file")
	addr := flag.String("addr", "", "listen address (overrides $PORT and config)")
	flag.Parse()

	log := clog.New(os.Getenv("COMPLIARY_LOG_LEVEL"))
	if err := run(*cfgPath, *addr, log); err != nil {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath, addrOverride string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Listen-address precedence: -addr flag > $PORT > default.
	listenAddr := ":8088"
	if port := os.Getenv("PORT"); port != "" {
		listenAddr = ":" + port
	}
	if addrOverride != "" {
		listenAddr = addrOverride
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Info("shutdown signal received", "signal", sig.String())
		signal.Stop(sigCh)
		cancel()
	}()

	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	emb, err := buildQueryEmbedder(log)
	if err != nil {
		return fmt.Errorf("build query embedder: %w", err)
	}

	retriever, err := retrieve.New(pool, emb, log)
	if err != nil {
		return fmt.Errorf("build retriever: %w", err)
	}

	// Auth + projection decision.
	token := strings.TrimSpace(os.Getenv("COMPLIARY_MCP_TOKEN"))
	projection := mcp.ProjectionFull
	if token == "" {
		log.Warn("COMPLIARY_MCP_TOKEN not set — serving REDUCED projection only (no body, title_original, or chunk content). Set the token to enable full projection for authenticated callers.")
		projection = mcp.ProjectionReduced
	} else {
		log.Info("MCP bearer-token auth enabled — authenticated callers get full projection")
	}

	scoreFloor := loadScoreFloor(ctx, pool, log)

	corpus := mcp.DBCorpus(pool)
	core := mcp.NewCore(retriever, corpus, log,
		mcp.WithProjection(projection),
		mcp.WithScoreFloor(scoreFloor),
	)

	behindProxy := envBool("COMPLIARY_TRUST_PROXY", false)
	var sopts []mcp.ServerOption
	sopts = append(sopts, mcp.WithVersion(version))
	if behindProxy {
		sopts = append(sopts, mcp.WithBehindProxy())
	}
	srv := mcp.NewServer(core, log, sopts...)

	return serve(ctx, listenAddr, srv, token, log)
}

func serve(ctx context.Context, addr string, srv *mcp.Server, token string, log *slog.Logger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Mount MCP endpoint with cross-origin protection.
	mcpHandler := crossOriginProtected(srv.HTTPHandler(), log)
	mux.Handle("/mcp", mcpHandler)

	// Security middleware stack.
	handler, stopEvictor := secure(mux, token, log)
	defer stopEvictor()

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		log.Info("server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("server shutdown", "err", err)
		}
	}()

	log.Info("compliary MCP server listening", "addr", addr, "endpoint", "/mcp")
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server stopped: %w", err)
	}
	return nil
}

// --- middleware ---------------------------------------------------------------

const maxRequestBody = 1 << 20 // 1 MiB

// secure wraps h with the public-facing defenses: rate limit → auth → body cap.
// /healthz is exempt from auth.
func secure(h http.Handler, token string, log *slog.Logger) (http.Handler, func()) {
	rl := newRateLimiter(
		envFloat("COMPLIARY_MCP_RATE_RPS", 50),
		envInt("COMPLIARY_MCP_RATE_BURST", 100),
		envBool("COMPLIARY_TRUST_PROXY", false),
	)
	stop := rl.startEvictor(10 * time.Minute)

	h = bodyLimit(h)
	h = bearerAuth(h, token, log)
	h = rl.middleware(h)
	h = securityHeaders(h)
	h = recoverPanic(h, log)
	return h, stop
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

// bearerAuth enforces COMPLIARY_MCP_TOKEN via Authorization: Bearer <token>.
// Empty token → no auth enforcement (reduced-projection mode already set).
// /healthz always bypasses auth.
func bearerAuth(next http.Handler, token string, log *slog.Logger) http.Handler {
	if token == "" {
		return next // no auth in reduced-projection mode
	}
	log.Info("MCP bearer auth enabled")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		presented := presentedToken(r)
		if !tokenMatch(presented, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func presentedToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func tokenMatch(got, want string) bool {
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// crossOriginProtected wraps the MCP handler with Go's stdlib cross-origin
// protection (MCP spec CSRF / DNS-rebinding defense).
func crossOriginProtected(h http.Handler, log *slog.Logger) http.Handler {
	cop := http.NewCrossOriginProtection()
	for _, o := range splitComma(os.Getenv("COMPLIARY_MCP_ALLOWED_ORIGINS")) {
		if err := cop.AddTrustedOrigin(o); err != nil {
			log.Warn("ignoring invalid COMPLIARY_MCP_ALLOWED_ORIGINS entry", "origin", o, "err", err)
		}
	}
	return cop.Handler(h)
}

func recoverPanic(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error("panic serving request — contained",
					"err", v, "path", r.URL.Path,
					"stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- rate limiter ------------------------------------------------------------

type rateLimiter struct {
	mu         sync.Mutex
	clients    map[string]*clientLimiter
	rps        rate.Limit
	burst      int
	trustProxy bool
}

type clientLimiter struct {
	lim  *rate.Limiter
	seen time.Time
}

func newRateLimiter(rps float64, burst int, trustProxy bool) *rateLimiter {
	return &rateLimiter{clients: make(map[string]*clientLimiter), rps: rate.Limit(rps), burst: burst, trustProxy: trustProxy}
}

func (rl *rateLimiter) limiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	c, ok := rl.clients[ip]
	if !ok {
		c = &clientLimiter{lim: rate.NewLimiter(rl.rps, rl.burst)}
		rl.clients[ip] = c
	}
	c.seen = time.Now()
	return c.lim
}

func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.limiter(clientIP(r, rl.trustProxy)).Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *rateLimiter) startEvictor(ttl time.Duration) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(ttl)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				cutoff := time.Now().Add(-ttl)
				rl.mu.Lock()
				for ip, c := range rl.clients {
					if c.seen.Before(cutoff) {
						delete(rl.clients, ip)
					}
				}
				rl.mu.Unlock()
			}
		}
	}()
	return func() { close(done) }
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// --- helpers -----------------------------------------------------------------

func buildQueryEmbedder(log *slog.Logger) (embed.Embedder, error) {
	modelPath := os.Getenv("COMPLIARY_ONNX_MODEL")
	if modelPath == "" {
		home, _ := os.UserHomeDir()
		modelPath = home + "/.cache/banhmi/qwen3-embedding/model_fp16.onnx"
	}
	tokenizerPath := os.Getenv("COMPLIARY_ONNX_TOKENIZER")
	if tokenizerPath == "" {
		home, _ := os.UserHomeDir()
		tokenizerPath = home + "/.cache/banhmi/qwen3-embedding/tokenizer.json"
	}
	libPath := os.Getenv("COMPLIARY_ONNX_LIB")

	e, err := onnxembed.New(onnxembed.Config{
		ModelPath:     modelPath,
		TokenizerPath: tokenizerPath,
		LibPath:       libPath,
		Dims:          1024,
		Model:         "Qwen/Qwen3-Embedding-0.6B",
	})
	if err != nil {
		log.Warn("ONNX query embedder unavailable — search will use BM25-only mode", "err", err)
		return nil, nil //nolint:nilerr
	}
	return e, nil
}

func loadScoreFloor(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) float64 {
	var value string
	err := pool.QueryRow(ctx,
		"SELECT value FROM config.setting WHERE key = 'search_abstain_floor'",
	).Scan(&value)
	if err != nil {
		log.Warn("score floor unavailable, using 0", "err", err)
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(value, "%f", &f); err != nil {
		log.Warn("score floor unparseable, using 0", "value", value, "err", err)
		return 0
	}
	return f
}

func splitComma(s string) []string {
	var out []string
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
