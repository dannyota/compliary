// Command pipeline runs compliary's ingestion stages over the data/ corpus.
//
// Usage:
//
//	pipeline                              # run manifest → extract → normalize
//	pipeline -stage manifest              # run manifest stage only
//	pipeline -stage extract               # run extract stage only
//	pipeline -stage normalize             # run normalize stage only
//	pipeline -config config/config.yaml   # custom config path
//
// Each stage iterates eligible rows, records per-row errors, continues on
// error, and exits non-zero with an N-succeeded/M-failed/K-skipped summary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"danny.vn/compliary/pkg/base/config"
	"danny.vn/compliary/pkg/base/db"
	clog "danny.vn/compliary/pkg/base/log"
	"danny.vn/compliary/pkg/extract"
	"danny.vn/compliary/pkg/manifest"
	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

func main() {
	cfgPath := flag.String("config", "config/config.yaml", "path to config file")
	stage := flag.String("stage", "", "run a single stage: manifest, extract, normalize (default: run all)")
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
		case "manifest", "extract", "normalize":
			stages = []string{stage}
		default:
			return fmt.Errorf("unknown stage %q (want manifest, extract, or normalize)", stage)
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
			log.Info("normalize stage: not yet implemented")
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
	dbingest.DBTX
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
