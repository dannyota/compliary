// Command mcp serves compliary's control-framework evidence over MCP (stdio
// transport, JSON-RPC 2.0) for local LLM/agent clients. Full projection
// always — local operator. stdout is the transport; all logging goes to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
	flag.Parse()

	log := clog.New(os.Getenv("COMPLIARY_LOG_LEVEL"))
	if err := run(*cfgPath, log); err != nil {
		log.Error("mcp", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string, log *slog.Logger) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	// Build query embedder (ONNX in-process, no GPU needed for queries).
	emb, err := buildQueryEmbedder(log)
	if err != nil {
		return fmt.Errorf("build query embedder: %w", err)
	}

	return serve(ctx, pool, emb, log)
}

func serve(ctx context.Context, pool *pgxpool.Pool, emb embed.Embedder, log *slog.Logger) error {
	retriever, err := retrieve.New(pool, emb, log)
	if err != nil {
		return fmt.Errorf("build retriever: %w", err)
	}

	// Raw-cosine abstention floor from config.setting, applied by the retriever.
	retriever.SetAbstainFloor(loadScoreFloor(ctx, pool, log))

	corpus := mcp.DBCorpus(pool)
	core := mcp.NewCore(retriever, corpus, log,
		mcp.WithProjection(mcp.ProjectionFull),
	)
	srv := mcp.NewServer(core, log, mcp.WithVersion(version))

	log.Info("compliary MCP server running (stdio)", "version", version)
	if err := srv.Run(ctx, &mcpsdk.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve mcp: %w", err)
	}
	log.Info("compliary MCP server stopped")
	return nil
}

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
		return nil, nil //nolint:nilerr // graceful degradation to BM25-only
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
