// Command eval runs compliary's retrieval-quality harness over a citation-keyed
// golden Q&A set. It loads the golden set, invokes the retriever for each case,
// and prints per-case + aggregate metrics -- recall@k, MRR@k,
// current-version precision, and abstention correctness. It exits non-zero when
// an aggregate metric falls below a configured floor so `make eval` can gate CI
// before defaults are locked.
//
// compliary is evidence-only: there is no answer model to score. The retrieval
// mode flag compares bm25/vector/hybrid first-stage ranking (hybrid = default).
//
// When no retriever is configured (pre-Task-4), the command validates the
// golden set and exercises the report pipeline in dry-run mode, exiting 0.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	goldendata "danny.vn/compliary/deploy/eval"
	"danny.vn/compliary/pkg/base/config"
	"danny.vn/compliary/pkg/base/db"
	"danny.vn/compliary/pkg/eval"
	ragembed "danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/embed/onnxembed"
	"danny.vn/compliary/pkg/rag/retrieve"
)

var errSkip = errors.New("eval skipped")
var errThreshold = errors.New("eval below threshold")

type opts struct {
	golden             string
	topK               int
	poolK              int
	vectorK            int
	bm25K              int
	rrfK               int
	docCap             int
	lexWeight          float64
	retrievalMode      string
	review             bool
	reviewHits         int
	reviewPreviewChars int
	outPath            string
	usePins            bool

	minRecall  float64
	minMRR     float64
	minCurrent float64
	minAbstain float64
}

func main() {
	var o opts
	flag.StringVar(&o.golden, "golden", "", "path to golden CSV (empty = use embedded)")
	flag.IntVar(&o.topK, "top-k", 8, "retriever top-k")
	flag.IntVar(&o.poolK, "pool-k", 0, "deep-probe candidate depth (0 = off)")
	flag.IntVar(&o.vectorK, "vector-k", 0, "vector arm candidate depth (0 = default)")
	flag.IntVar(&o.bm25K, "bm25-k", 0, "BM25 arm candidate depth (0 = default)")
	flag.IntVar(&o.rrfK, "rrf-k", 0, "RRF fusion constant (0 = default)")
	flag.IntVar(&o.docCap, "doc-cap", 0, "per-framework hit cap (0 = default)")
	flag.Float64Var(&o.lexWeight, "lex-weight", 0, "BM25 arm RRF weight (0 = default)")
	flag.StringVar(&o.retrievalMode, "retrieval-mode", "hybrid", "bm25, vector, or hybrid")
	flag.BoolVar(&o.review, "review", false, "print per-case evidence review")
	flag.IntVar(&o.reviewHits, "review-hits", 3, "top hits per case in review mode")
	flag.IntVar(&o.reviewPreviewChars, "review-preview-chars", 240, "max content preview chars per hit")
	flag.StringVar(&o.outPath, "out", "", "write JSON report to this path (empty = off)")
	flag.BoolVar(&o.usePins, "use-pins", true, "pass framework/version pins from golden case metadata to SearchOpts")
	// Floors: Phase A baseline (2026-07-20, hybrid ONNX Qwen3, 105 queries / 100 scored):
	//   Open-corpus: recall@8=65.0%, MRR@8=43.1%, current=100%, abstain=95.2%
	//   Filtered:    recall@8=81.0%, MRR@8=62.5%, current=94.2%, abstain=95.2%
	// Floors set ~2pp under open-corpus lane (the hard mode, no pins):
	//   CGO_LDFLAGS="-L$HOME/.local/lib" COMPLIARY_ONNX_LIB=$HOME/.local/lib/libonnxruntime.so \
	//   COMPLIARY_DATABASE_PASSWORD=compliary go run -tags onnx ./cmd/eval \
	//   -min-recall 0.63 -min-mrr 0.41 -min-current 0.98 -min-abstain 0.93
	flag.Float64Var(&o.minRecall, "min-recall", 0, "fail if recall@k below this (0 = no gate)")
	flag.Float64Var(&o.minMRR, "min-mrr", 0, "fail if mrr@k below this (0 = no gate)")
	flag.Float64Var(&o.minCurrent, "min-current", 0, "fail if current-version precision below this (0 = no gate)")
	flag.Float64Var(&o.minAbstain, "min-abstain", 0, "fail if abstention accuracy below this (0 = no gate)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	switch err := run(o, log); {
	case err == nil:
		// passed
	case errors.Is(err, errSkip):
		log.Warn("eval skipped", "reason", err)
	case errors.Is(err, errThreshold):
		log.Error("eval gate failed", "err", err)
		os.Exit(1)
	default:
		log.Error("eval", "err", err)
		os.Exit(1)
	}
}

func run(o opts, log *slog.Logger) error {
	if _, err := eval.ParseSearchMode(o.retrievalMode); err != nil {
		return err
	}
	if o.reviewHits <= 0 {
		return fmt.Errorf("-review-hits must be positive")
	}
	if o.reviewPreviewChars <= 0 {
		return fmt.Errorf("-review-preview-chars must be positive")
	}
	if o.poolK < 0 {
		return fmt.Errorf("-pool-k must be non-negative")
	}
	if o.docCap < 0 {
		return fmt.Errorf("-doc-cap must be non-negative")
	}

	var cases []eval.Case
	var err error
	if o.golden != "" {
		cases, err = eval.LoadGolden(o.golden)
	} else {
		cases, err = eval.LoadGoldenEmbed(goldendata.GoldenCSV, "deploy/eval/golden.csv")
	}
	if err != nil {
		return err
	}
	log.Info("loaded golden set", "cases", len(cases))

	// Connect to the database.
	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		log.Warn("no database configured; running in dry-run mode", "err", err)
		return dryRun(o, cases, log)
	}
	defer pool.Close()

	// Build the embedder if ONNX model is available.
	var embedder ragembed.Embedder
	onnxModel := envOrDefault("COMPLIARY_ONNX_MODEL",
		filepath.Join(os.Getenv("HOME"), ".cache/banhmi/qwen3-embedding/model_fp16.onnx"))
	onnxTokenizer := envOrDefault("COMPLIARY_ONNX_TOKENIZER",
		filepath.Join(os.Getenv("HOME"), ".cache/banhmi/qwen3-embedding/tokenizer.json"))
	onnxLib := os.Getenv("COMPLIARY_ONNX_LIB")

	// Build seam: this file has no onnx build tag — under !onnx the disabled
	// stub's New(Config) returns an error and the retriever degrades to
	// BM25-only. Config must therefore never gain fields typed from
	// ONNX-only dependencies.
	embedder, err = onnxembed.New(onnxembed.Config{
		ModelPath:     onnxModel,
		TokenizerPath: onnxTokenizer,
		LibPath:       onnxLib,
		Dims:          1024,
		Model:         "qwen3-embedding-0.6b",
		NumKVLayers:   28,
		NumKVHeads:    8,
		HeadDim:       128,
	})
	if err != nil {
		log.Warn("ONNX embedder not available, running BM25-only", "err", err)
		embedder = nil
	}

	retriever, err := retrieve.New(pool, embedder, log)
	if err != nil {
		return fmt.Errorf("build retriever: %w", err)
	}

	isCurrent := func(h eval.Hit) bool { return h.IsCurrent }
	return evaluate(ctx, o, cases, retriever, isCurrent, log)
}

// dryRun validates the golden set and reports what would be evaluated. This is
// the pre-Task-4 mode: confirms the harness compiles, the golden set is valid,
// and the metrics/report machinery works. Once the retriever is wired, this
// path becomes the empty-corpus skip.
func dryRun(o opts, cases []eval.Case, log *slog.Logger) error {
	var inScope, abstain, expectFail int
	for _, c := range cases {
		switch {
		case c.ExpectAbstain:
			abstain++
		case c.ExpectFail:
			expectFail++
		default:
			inScope++
		}
	}
	log.Info("golden set summary",
		"total", len(cases),
		"in_scope", inScope,
		"abstain", abstain,
		"expect_fail", expectFail,
	)

	// Generate a synthetic result set with zero scores to exercise the
	// report pipeline.
	matcher := eval.Matcher{}
	results := make([]eval.CaseResult, len(cases))
	for i, c := range cases {
		results[i] = eval.Score(c, nil, c.ExpectAbstain, nil, matcher)
	}
	agg := eval.Summarize(results)
	eval.WriteReport(os.Stdout, results, agg)

	if o.outPath != "" {
		meta := eval.JSONReportMeta{
			RetrievalMode: o.retrievalMode,
			TopK:          o.topK,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
			Chunks:        0,
		}
		if err := writeJSONReportFile(o.outPath, meta, results, agg); err != nil {
			return err
		}
		log.Info("wrote JSON report", "path", o.outPath)
	}

	// In dry-run mode, skip threshold checks (no real retrieval data).
	return nil
}

// evaluate is the full eval loop, invoked when a Retriever is available.
// It runs two lanes when -use-pins is true:
//   - open-corpus: no framework/version pins (the hard mode, baseline floors apply here)
//   - framework-filtered: passes framework (and version for pin cases) from golden metadata
//
// Floors gate the open-corpus lane only.
func evaluate(
	ctx context.Context,
	o opts,
	cases []eval.Case,
	r eval.Retriever,
	isCurrent eval.CurrentFn,
	log *slog.Logger,
) error {
	matcher := eval.Matcher{}

	// --- Open-corpus lane (always runs) ---
	results := make([]eval.CaseResult, 0, len(cases))
	var reviewRuns []reviewRun
	for _, c := range cases {
		searchOpts := eval.SearchOpts{
			TopK:      o.topK,
			VectorK:   o.vectorK,
			BM25K:     o.bm25K,
			RRFK:      o.rrfK,
			DocCap:    o.docCap,
			LexWeight: o.lexWeight,
			Mode:      eval.SearchMode(o.retrievalMode),
		}
		ev, err := r.SearchEvidence(ctx, c.Question, searchOpts)
		if err != nil {
			return fmt.Errorf("retrieve case %q: %w", c.ID, err)
		}
		hits := ev.Hits
		abstained := ev.Abstain || len(hits) == 0
		result := eval.Score(c, hits, abstained, isCurrent, matcher)

		if o.poolK > 0 && len(c.ExpectedCitations) > 0 {
			poolOpts := eval.SearchOpts{
				TopK:    o.poolK,
				VectorK: o.poolK,
				BM25K:   o.poolK,
				DocCap:  o.poolK,
				Mode:    eval.SearchMode(o.retrievalMode),
			}
			poolEv, err := r.SearchEvidence(ctx, c.Question, poolOpts)
			if err != nil {
				return fmt.Errorf("pool probe case %q: %w", c.ID, err)
			}
			_, result.PoolHits, result.PoolWant = eval.Recall(c, poolEv.Hits, matcher)
			_, result.PoolRank = eval.ReciprocalRank(c, poolEv.Hits, matcher)
		}
		results = append(results, result)
		if o.review {
			reviewRuns = append(reviewRuns, reviewRun{
				Case:   c,
				Hits:   append([]eval.Hit(nil), hits...),
				Gaps:   append([]eval.Gap(nil), ev.Gaps...),
				Result: result,
			})
		}
	}

	agg := eval.Summarize(results)
	_, _ = fmt.Fprintln(os.Stdout, "=== Open-corpus lane (no pins) ===")
	eval.WriteReport(os.Stdout, results, agg)

	// --- Framework-filtered lane (when -use-pins is true) ---
	var filteredAgg eval.Aggregate
	if o.usePins {
		filteredResults := make([]eval.CaseResult, 0, len(cases))
		for _, c := range cases {
			searchOpts := eval.SearchOpts{
				TopK:      o.topK,
				VectorK:   o.vectorK,
				BM25K:     o.bm25K,
				RRFK:      o.rrfK,
				DocCap:    o.docCap,
				LexWeight: o.lexWeight,
				Mode:      eval.SearchMode(o.retrievalMode),
			}
			// Pin framework from golden case metadata.
			if len(c.ExpectedCitations) > 0 {
				ec := c.ExpectedCitations[0]
				searchOpts.Framework = ec.FrameworkCode
				searchOpts.VersionLabel = ec.VersionLabel
			}
			// Include withdrawn controls when the notes tag signals it.
			if strings.Contains(strings.ToLower(c.Notes), "withdrawn") {
				searchOpts.IncludeWithdrawn = true
			}
			ev, err := r.SearchEvidence(ctx, c.Question, searchOpts)
			if err != nil {
				return fmt.Errorf("filtered lane case %q: %w", c.ID, err)
			}
			hits := ev.Hits
			abstained := ev.Abstain || len(hits) == 0
			result := eval.Score(c, hits, abstained, isCurrent, matcher)
			filteredResults = append(filteredResults, result)
		}
		filteredAgg = eval.Summarize(filteredResults)
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "=== Framework-filtered lane (pins from golden metadata) ===")
		eval.WriteReport(os.Stdout, filteredResults, filteredAgg)
	}

	if o.outPath != "" {
		meta := eval.JSONReportMeta{
			RetrievalMode: o.retrievalMode,
			TopK:          o.topK,
			PoolK:         o.poolK,
			DocCap:        o.docCap,
			GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		if err := writeJSONReportFile(o.outPath, meta, results, agg); err != nil {
			return err
		}
		log.Info("wrote JSON report", "path", o.outPath)
	}

	if o.review {
		writeReview(os.Stdout, o, reviewRuns, matcher)
	}

	// Floors gate the open-corpus lane only.
	thresholds := eval.Thresholds{
		MinRecall:  o.minRecall,
		MinMRR:     o.minMRR,
		MinCurrent: o.minCurrent,
		MinAbstain: o.minAbstain,
	}
	if fails := thresholds.Check(agg); len(fails) > 0 {
		for _, f := range fails {
			log.Error("threshold not met", "metric", f.Metric,
				"got", fmt.Sprintf("%.3f", f.Got), "want", fmt.Sprintf("%.3f", f.Want))
		}
		return fmt.Errorf("%w: %d metric(s) below floor", errThreshold, len(fails))
	}

	// Log filtered lane summary if it ran.
	if o.usePins {
		log.Info("framework-filtered lane",
			"recall", fmt.Sprintf("%.1f%%", filteredAgg.RecallAtK*100),
			"mrr", fmt.Sprintf("%.1f%%", filteredAgg.MRRAtK*100),
		)
	}

	return nil
}

type reviewRun struct {
	Case   eval.Case
	Hits   []eval.Hit
	Gaps   []eval.Gap
	Result eval.CaseResult
}

func writeReview(w io.Writer, o opts, runs []reviewRun, matcher eval.Matcher) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Evidence review")
	_, _ = fmt.Fprintln(w, "---------------")
	for _, run := range runs {
		_, _ = fmt.Fprintf(w, "\n[%s] %s\n", run.Case.ID, run.Case.Question)
		for _, gap := range run.Gaps {
			block := "warning"
			if gap.BlocksAnswer {
				block = "blocking"
			}
			_, _ = fmt.Fprintf(w, "  gap: %s %s | %s\n", block, gap.Kind, gap.Message)
		}
		limit := o.reviewHits
		if len(run.Hits) < limit {
			limit = len(run.Hits)
		}
		if limit == 0 {
			_, _ = fmt.Fprintln(w, "  no hits")
			continue
		}
		for i, h := range run.Hits[:limit] {
			match := ""
			if matcher.MatchesAny(run.Case, h) {
				match = " expected"
			}
			_, _ = fmt.Fprintf(w, "  %d.%s %s/%s %s | score %.5f\n",
				i+1, match, h.FrameworkCode, h.VersionLabel, h.CitationNorm, h.Score)
			_, _ = fmt.Fprintf(w, "     %s\n", previewText(h.Content, o.reviewPreviewChars))
		}
	}
}

func previewText(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	return string(rs[:maxRunes-1]) + "..."
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func writeJSONReportFile(path string, meta eval.JSONReportMeta, results []eval.CaseResult, agg eval.Aggregate) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := eval.WriteJSONReport(f, meta, results, agg); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return nil
}
