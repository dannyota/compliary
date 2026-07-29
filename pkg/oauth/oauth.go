package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/time/rate"
)

// Server is an OAuth 2.0 authorization server for compliary's MCP endpoint. It
// issues opaque tokens and verifies the local operator identity via a
// bcrypt-hashed password.
type Server struct {
	issuer       string
	operatorHash []byte
	store        oauthStore
	authorizer   Authorizer
	log          *slog.Logger
	stopEvictor  func()
	cimdLimiter  *rate.Limiter // rate-limits outbound CIMD metadata fetches
}

// Option configures a Server.
type Option func(*Server)

// WithAuthorizer replaces the default allow-all authorization policy.
func WithAuthorizer(authorizer Authorizer) Option {
	return func(s *Server) {
		if authorizer != nil {
			s.authorizer = authorizer
		}
	}
}

// New creates a PostgreSQL-backed OAuth server. PostgreSQL is required so
// production grants survive restarts and remain consistent across instances.
func New(issuer string, operatorHash []byte, pool *pgxpool.Pool, log *slog.Logger, opts ...Option) (*Server, error) {
	if pool == nil {
		return nil, errors.New("oauth postgres pool is required")
	}
	store := newPostgresStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.checkSchema(ctx); err != nil {
		return nil, err
	}
	return newServer(issuer, operatorHash, store, log, opts...), nil
}

func newMemoryServer(issuer string, operatorHash []byte, log *slog.Logger, opts ...Option) *Server {
	return newServer(issuer, operatorHash, newStore(), log, opts...)
}

func newServer(issuer string, operatorHash []byte, store oauthStore, log *slog.Logger, opts ...Option) *Server {
	s := &Server{
		issuer:       issuer,
		operatorHash: operatorHash,
		store:        store,
		authorizer:   AllowAllAuthorizer{},
		log:          log,
		cimdLimiter:  rate.NewLimiter(1, 5), // 1/s sustained, burst 5
	}
	for _, opt := range opts {
		opt(s)
	}
	s.stopEvictor = s.startEvictor(5 * time.Minute)
	return s
}

func (s *Server) startEvictor(interval time.Duration) func() {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				err := s.store.evict(ctx)
				cancel()
				if err != nil && s.log != nil {
					s.log.Error("OAuth cleanup failed", slog.String("error", err.Error()))
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

// StopEvictor stops the background expired-entry cleanup goroutine.
func (s *Server) StopEvictor() {
	if s.stopEvictor != nil {
		s.stopEvictor()
	}
}

// Handler returns an http.Handler with all OAuth routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Protected Resource Metadata (RFC 9728).
	mux.Handle("GET /.well-known/oauth-protected-resource",
		auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               s.issuer,
			AuthorizationServers:   []string{s.issuer},
			ScopesSupported:        []string{"mcp:read"},
			BearerMethodsSupported: []string{"header"},
			ResourceName:           "compliary MCP",
			ResourceDocumentation:  "https://github.com/dannyota/compliary",
		}))

	// Authorization Server Metadata (RFC 8414).
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadataHandler)

	// Empty JWKS (opaque tokens).
	mux.HandleFunc("GET /oauth/jwks", s.jwksHandler)

	// Dynamic Client Registration (RFC 7591).
	mux.HandleFunc("POST /oauth/register", s.registerHandler)

	// Authorization endpoint.
	mux.HandleFunc("/oauth/authorize", s.authorizeHandler)

	// Token endpoint.
	mux.HandleFunc("POST /oauth/token", s.tokenHandler)

	return mux
}

// TokenVerifier validates opaque access tokens, reapplies the current
// authorization policy, and renews valid sessions for the 30-day inactivity
// window.
func (s *Server) TokenVerifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		te, ok, err := s.store.lookupToken(ctx, token)
		if err != nil {
			if s.log != nil {
				s.log.Error("OAuth token lookup failed", slog.String("error", err.Error()))
			}
			return nil, fmt.Errorf("%w: token verification unavailable", auth.ErrInvalidToken)
		}
		if !ok {
			return nil, fmt.Errorf("%w: unknown or expired token", auth.ErrInvalidToken)
		}
		if err := s.authorizer.Authorize(ctx, te.Principal); err != nil {
			return nil, fmt.Errorf("%w: principal is not authorized", auth.ErrInvalidToken)
		}
		te, ok, err = s.store.renewToken(ctx, token)
		if err != nil {
			if s.log != nil {
				s.log.Error("OAuth token renewal failed", slog.String("error", err.Error()))
			}
			return nil, fmt.Errorf("%w: token verification unavailable", auth.ErrInvalidToken)
		}
		if !ok {
			return nil, fmt.Errorf("%w: unknown, expired, or revoked token", auth.ErrInvalidToken)
		}
		return &auth.TokenInfo{
			Scopes:     splitScope(te.Scope),
			Expiration: te.ExpiresAt,
		}, nil
	}
}

// BearerFallback returns a combined [auth.TokenVerifier] that first tries
// the OAuth token store, then falls back to matching a static bearer token
// (for backward compatibility with COMPLIARY_MCP_TOKEN).
func (s *Server) BearerFallback(staticToken string) auth.TokenVerifier {
	oauthVerify := s.TokenVerifier()
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		// Try OAuth store first.
		info, err := oauthVerify(ctx, token, req)
		if err == nil {
			return info, nil
		}

		// Fall back to static bearer token.
		if subtle.ConstantTimeCompare([]byte(token), []byte(staticToken)) == 1 {
			return &auth.TokenInfo{
				Scopes:     []string{"mcp:read"},
				Expiration: time.Now().Add(tokenIdleTTL),
			}, nil
		}

		return nil, fmt.Errorf("%w: invalid token", auth.ErrInvalidToken)
	}
}

func splitScope(scope string) []string {
	if scope == "" {
		return nil
	}
	return strings.Fields(scope)
}
