// Command pipeline runs compliary's ingestion stages over the data/ corpus.
//
// Usage:
//
//	pipeline                              # run manifest → extract → normalize
//	pipeline -stage manifest              # run manifest stage only
//	pipeline -stage extract               # run extract stage only
//	pipeline -stage normalize             # run normalize stage only
//	pipeline -stage index                 # run index stage only
//	pipeline -config config/config.yaml   # custom config path
//
// Each stage iterates eligible rows, records per-row errors, continues on
// error, and exits non-zero with an N-succeeded/M-failed/K-skipped summary.
//
// Index stage environment variables:
//
//	COMPLIARY_ONNX_MODEL      path to model_fp16.onnx (default ~/.cache/banhmi/qwen3-embedding/model_fp16.onnx)
//	COMPLIARY_ONNX_TOKENIZER  path to tokenizer.json  (default ~/.cache/banhmi/qwen3-embedding/tokenizer.json)
//	COMPLIARY_ONNX_LIB        path to libonnxruntime.so (optional; empty = default search)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"danny.vn/compliary/pkg/base/config"
	"danny.vn/compliary/pkg/base/db"
	clog "danny.vn/compliary/pkg/base/log"
	"danny.vn/compliary/pkg/extract"
	"danny.vn/compliary/pkg/manifest"
	"danny.vn/compliary/pkg/normalize"
	"danny.vn/compliary/pkg/rag/embed/kagglebatch"
	"danny.vn/compliary/pkg/rag/embed/onnxembed"
	ragindex "danny.vn/compliary/pkg/rag/index"
	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbgold "danny.vn/compliary/pkg/store/gold"
	dbingest "danny.vn/compliary/pkg/store/ingest"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

func main() {
	cfgPath := flag.String("config", "config/config.yaml", "path to config file")
	stage := flag.String("stage", "", "run a single stage: manifest, extract, normalize, index (default: run manifest+extract+normalize)")
	flag.Parse()

	log := clog.New(os.Getenv("COMPLIARY_LOG_LEVEL"))
	if err := run(*cfgPath, *stage, log); err != nil {
		log.Error("pipeline", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath, stage string, log *slog.Logger) error {
	ctx := context.Background()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Info("compliary pipeline", "db", cfg.Database.Redacted(), "data_dir", cfg.DataDir)

	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	stages := []string{"manifest", "extract", "normalize"}
	if stage != "" {
		switch stage {
		case "manifest", "extract", "normalize", "index":
			stages = []string{stage}
		default:
			return fmt.Errorf("unknown stage %q (want manifest, extract, normalize, or index)", stage)
		}
	}

	var hasError bool
	for _, s := range stages {
		switch s {
		case "manifest":
			if err := runManifest(ctx, cfg, pool, log); err != nil {
				log.Error("manifest stage failed", "err", err)
				hasError = true
			}
		case "extract":
			if err := runExtract(ctx, cfg, pool, log); err != nil {
				log.Error("extract stage failed", "err", err)
				hasError = true
			}
		case "normalize":
			if err := runNormalize(ctx, pool, log); err != nil {
				log.Error("normalize stage failed", "err", err)
				hasError = true
			}
		case "index":
			if err := runIndex(ctx, cfg, pool, log); err != nil {
				log.Error("index stage failed", "err", err)
				hasError = true
			}
		}
	}

	if hasError {
		return fmt.Errorf("pipeline completed with errors")
	}
	return nil
}

type poolWrapper interface {
	dbbronze.DBTX
	dbconfig.DBTX
	dbgold.DBTX
	dbingest.DBTX
	dbsilver.DBTX
}

func runManifest(ctx context.Context, cfg *config.Config, pool poolWrapper, log *slog.Logger) error {
	// Load file rules from config.
	cfgQ := dbconfig.New(pool)
	allRules, err := cfgQ.ListAllFileRules(ctx)
	if err != nil {
		return fmt.Errorf("load file rules: %w", err)
	}
	rules := manifest.RulesFromDB(allRules)
	log.Info("loaded file rules", "count", len(rules))

	// Run the manifest scanner.
	ingQ := dbingest.New(pool)
	scanner := &manifest.Scanner{
		DataDir: cfg.DataDir,
		Rules:   rules,
		Log:     log,
	}
	sum, err := scanner.Scan(ctx, ingQ)
	if err != nil {
		return fmt.Errorf("manifest scan: %w", err)
	}

	log.Info("manifest scan complete",
		"total", sum.Total,
		"matched", sum.Matched,
		"ignored", sum.Ignored,
		"unrecognized", sum.Unrecognized,
		"ambiguous", sum.Ambiguous,
		"demoted", sum.Demoted,
		"failed", sum.Failed,
	)

	if sum.Ambiguous > 0 {
		return fmt.Errorf("manifest: %d ambiguous files detected", sum.Ambiguous)
	}
	if sum.Failed > 0 {
		return fmt.Errorf("manifest: %d files failed to read", sum.Failed)
	}

	return nil
}

func runExtract(ctx context.Context, cfg *config.Config, pool poolWrapper, log *slog.Logger) error {
	// Load file rules from config (for provenance re-match).
	cfgQ := dbconfig.New(pool)
	allRules, err := cfgQ.ListAllFileRules(ctx)
	if err != nil {
		return fmt.Errorf("load file rules: %w", err)
	}
	rules := manifest.RulesFromDB(allRules)

	// List eligible rows.
	ingQ := dbingest.New(pool)
	files, err := ingQ.ListFilesToExtract(ctx)
	if err != nil {
		return fmt.Errorf("list files to extract: %w", err)
	}
	log.Info("extract: eligible files", "count", len(files))

	if len(files) == 0 {
		log.Info("extract: nothing to do")
		return nil
	}

	bronzeQ := dbbronze.New(pool)
	ext := &extract.Extractor{
		DataDir: cfg.DataDir,
		Rules:   rules,
		Log:     log,
	}
	sum, err := ext.Run(ctx, files, ingQ, bronzeQ, cfgQ)
	if err != nil {
		return fmt.Errorf("extract run: %w", err)
	}

	log.Info("extract complete",
		"succeeded", sum.Succeeded,
		"failed", sum.Failed,
		"skipped", sum.Skipped,
	)

	if sum.Failed > 0 {
		return fmt.Errorf("extract: %d files failed", sum.Failed)
	}
	return nil
}

func runNormalize(ctx context.Context, pool poolWrapper, log *slog.Logger) error {
	ingQ := dbingest.New(pool)
	files, err := ingQ.ListFilesToNormalize(ctx)
	if err != nil {
		return fmt.Errorf("list files to normalize: %w", err)
	}
	log.Info("normalize: eligible files", "count", len(files))

	if len(files) == 0 {
		log.Info("normalize: nothing to do")
		return nil
	}

	bronzeQ := dbbronze.New(pool)
	silverQ := dbsilver.New(pool)
	cfgQ := dbconfig.New(pool)

	norm := &normalize.Normalizer{Log: log}
	sum, err := norm.Run(ctx, files, ingQ, bronzeQ, silverQ, cfgQ)
	if err != nil {
		return fmt.Errorf("normalize run: %w", err)
	}

	log.Info("normalize complete",
		"succeeded", sum.Succeeded,
		"failed", sum.Failed,
		"skipped", sum.Skipped,
	)

	if sum.Failed > 0 {
		return fmt.Errorf("normalize: %d files failed", sum.Failed)
	}
	return nil
}

// defaultONNXModelDir returns ~/.cache/banhmi/qwen3-embedding — the shared
// default model directory (same model assets as banhmi). Returns an error if
// the user home directory cannot be determined.
func defaultONNXModelDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "banhmi", "qwen3-embedding"), nil
}

// initONNXEmbedder lazily creates the ONNX embedder. Called only when
// embeddings are actually needed (after chunk building), so chunk building
// works without -tags onnx.
func initONNXEmbedder(log *slog.Logger) (ragindex.Embedder, error) {
	modelDir, err := defaultONNXModelDir()
	if err != nil {
		return nil, err
	}
	modelPath := os.Getenv("COMPLIARY_ONNX_MODEL")
	if modelPath == "" {
		modelPath = filepath.Join(modelDir, "model_fp16.onnx")
	}
	tokPath := os.Getenv("COMPLIARY_ONNX_TOKENIZER")
	if tokPath == "" {
		tokPath = filepath.Join(modelDir, "tokenizer.json")
	}
	libPath := os.Getenv("COMPLIARY_ONNX_LIB")

	// Verify model assets exist before proceeding.
	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("ONNX model not found at %s — set COMPLIARY_ONNX_MODEL or place model at default path", modelPath)
	}
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("ONNX tokenizer not found at %s — set COMPLIARY_ONNX_TOKENIZER or place tokenizer at default path", tokPath)
	}
	log.Info("index: ONNX model", "model", modelPath, "tokenizer", tokPath)

	embedder, err := onnxembed.New(onnxembed.Config{
		ModelPath:     modelPath,
		TokenizerPath: tokPath,
		LibPath:       libPath,
	})
	if err != nil {
		return nil, fmt.Errorf("init ONNX embedder: %w", err)
	}
	return embedder, nil
}

func runIndex(ctx context.Context, cfg *config.Config, pool poolWrapper, log *slog.Logger) error {
	// List files eligible for indexing (normalized but not yet indexed).
	ingQ := dbingest.New(pool)
	files, err := ingQ.ListFilesToIndex(ctx)
	if err != nil {
		return fmt.Errorf("list files to index: %w", err)
	}
	log.Info("index: eligible files", "count", len(files))

	if len(files) == 0 {
		log.Info("index: nothing to do")
		return nil
	}

	silverQ := dbsilver.New(pool)
	goldQ := dbgold.New(pool)

	// Chunk building needs no embedder — defer init to embed phase.
	idx := &ragindex.Indexer{
		Log:       log,
		BatchSize: 32,
	}

	// Reap orphan chunks whose control_id no longer exists in silver.
	if _, err := idx.ReapOrphans(ctx, goldQ); err != nil {
		log.Error("index: reap orphans", "err", err)
		return fmt.Errorf("index: reap orphans: %w", err)
	}

	// Collect distinct framework codes for the licensing warning.
	frameworkSet := make(map[string]bool)
	var totalChunks, totalEmbeddings int
	var hasError bool

	for _, f := range files {
		fc := *f.FrameworkCode
		vl := *f.VersionLabel
		frameworkSet[fc] = true

		// Per-file error flag — MarkIndexed only on full success.
		fileErrored := false

		// Find the silver document for this file.
		docs, err := silverQ.ListDocumentsForVersion(ctx, dbsilver.ListDocumentsForVersionParams{
			FrameworkCode: fc,
			VersionLabel:  vl,
		})
		if err != nil {
			log.Error("index: list documents", "file", f.RelPath, "err", err)
			_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{ID: f.ID, StageError: "index: " + err.Error()})
			hasError = true
			continue
		}
		if len(docs) == 0 {
			log.Warn("index: no silver document", "file", f.RelPath, "framework", fc, "version", vl)
			_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{ID: f.ID, StageError: "index: no silver document found"})
			hasError = true
			continue
		}

		for _, doc := range docs {
			controls, err := silverQ.ListControlsForDocument(ctx, doc.ID)
			if err != nil {
				log.Error("index: list controls", "doc", doc.DocKey, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{ID: f.ID, StageError: "index: " + err.Error()})
				fileErrored = true
				hasError = true
				continue
			}
			if len(controls) == 0 {
				log.Info("index: no controls", "doc", doc.DocKey)
				continue
			}

			created, err := idx.BuildChunks(ctx, doc, controls, goldQ)
			if err != nil {
				log.Error("index: build chunks", "doc", doc.DocKey, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{ID: f.ID, StageError: "index: " + err.Error()})
				fileErrored = true
				hasError = true
				continue
			}
			totalChunks += created
			log.Info("index: chunks built", "doc", doc.DocKey, "chunks", created)
		}

		// Mark indexed only when every document for this file succeeded.
		if fileErrored {
			continue
		}
		if err := ingQ.MarkIndexed(ctx, f.ID); err != nil {
			log.Error("index: mark indexed", "file", f.RelPath, "err", err)
			hasError = true
		}
	}

	// Engine selection for the embed phase.
	const embedModel = "qwen3-embedding-0.6b"
	const embedDims = 1024

	engine := cfg.EmbedEngine()

	// For "auto" with Kaggle token, check MinBatch threshold.
	if engine == "kaggle" && cfg.Embed.Engine == "auto" {
		missingCount, err := idx.CountMissing(ctx, goldQ, embedModel)
		if err != nil {
			log.Error("index: count missing embeddings", "err", err)
			hasError = true
		} else if missingCount < cfg.Embed.Kaggle.MinBatch {
			log.Info("index: missing chunks below min_batch, falling back to local",
				"missing", missingCount, "min_batch", cfg.Embed.Kaggle.MinBatch)
			engine = "local"
		}
	}

	log.Info("index: embed engine selected", "engine", engine)

	var frameworks []string
	for fc := range frameworkSet {
		frameworks = append(frameworks, fc)
	}

	switch engine {
	case "kaggle":
		batch, err := kagglebatch.New(kagglebatch.Options{
			Owner:        cfg.Embed.Kaggle.Owner,
			ModelDataset: cfg.Embed.Kaggle.ModelDataset,
			Accelerator:  cfg.Embed.Kaggle.Accelerator,
			Dims:         embedDims,
			Token:        cfg.KaggleToken,
		}, log)
		if err != nil {
			log.Error("index: kaggle embedder init", "err", err)
			hasError = true
		} else {
			embedded, err := idx.EmbedMissingKaggle(ctx, goldQ, batch, embedModel, embedDims, frameworks)
			if err != nil {
				log.Error("index: kaggle embed", "err", err)
				hasError = true
			}
			totalEmbeddings += embedded
		}

	default: // "local"
		embedder, err := initONNXEmbedder(log)
		if err != nil {
			log.Error("index: embedder init deferred", "err", err)
			hasError = true
		} else {
			idx.Embedder = embedder
			embedded, err := idx.EmbedMissing(ctx, goldQ)
			if err != nil {
				log.Error("index: embed", "err", err)
				hasError = true
			}
			totalEmbeddings += embedded
		}
	}

	log.Info("index complete",
		"chunks_created", totalChunks,
		"embeddings_upserted", totalEmbeddings,
	)

	if hasError {
		return fmt.Errorf("index: completed with errors")
	}
	return nil
}
