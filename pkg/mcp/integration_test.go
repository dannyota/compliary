package mcp

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	ragembed "danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/embed/onnxembed"
	"danny.vn/compliary/pkg/rag/retrieve"
)

// TestIntegration_LiveCorpus exercises all five tools against the real dev
// corpus (port 10011). It is the Task 1 live-validation gate. Skipped when the
// DB or ONNX model is unavailable (other operators).
func TestIntegration_LiveCorpus(t *testing.T) {
	pool := testPool(t)

	embedder := testEmbedder(t)
	retriever, err := retrieve.New(pool, embedder, nil)
	if err != nil {
		t.Fatalf("build retriever: %v", err)
	}

	corpus := DBCorpus(pool)
	core := NewCore(retriever, corpus, nil)
	ctx := context.Background()

	// --- guide ---
	t.Run("guide", func(t *testing.T) {
		g := core.Guide()
		if g.Purpose == "" {
			t.Error("purpose empty")
		}
		if len(g.Tools) != 5 {
			t.Errorf("tools: got %d, want 5", len(g.Tools))
		}
		t.Logf("guide: %d tools, %d flow steps, %d contract points",
			len(g.Tools), len(g.RecommendedFlow), len(g.EvidenceContract))
	})

	// --- corpus_status ---
	t.Run("corpus_status", func(t *testing.T) {
		out, err := core.CorpusStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !out.SearchReady {
			t.Error("corpus not search-ready")
		}
		if out.Totals.Frameworks < 10 {
			t.Errorf("expected >=10 frameworks, got %d", out.Totals.Frameworks)
		}
		if out.Totals.Controls < 3000 {
			t.Errorf("expected >=3000 controls, got %d", out.Totals.Controls)
		}
		if out.Totals.Chunks < 3000 {
			t.Errorf("expected >=3000 chunks, got %d", out.Totals.Chunks)
		}
		if out.Totals.MappingEdges < 2000 {
			t.Errorf("expected >=2000 mapping edges, got %d", out.Totals.MappingEdges)
		}
		t.Logf("corpus_status: %d frameworks, %d versions, %d controls, %d chunks, %d edges (%d resolved, %d unresolved)",
			out.Totals.Frameworks, out.Totals.Versions, out.Totals.Controls, out.Totals.Chunks,
			out.Totals.MappingEdges, out.Totals.Resolved, out.Totals.Unresolved)

		// Spot-check one framework.
		foundNIST := false
		for _, fvs := range out.Frameworks {
			if fvs.FrameworkCode == "nist80053" && fvs.VersionLabel == "r5" {
				foundNIST = true
				if fvs.Controls < 1100 {
					t.Errorf("nist80053/r5: expected >=1100 controls, got %d", fvs.Controls)
				}
				t.Logf("nist80053/r5: %d controls (%d withdrawn), %d chunks, %d edges (%d resolved)",
					fvs.Controls, fvs.Withdrawn, fvs.Chunks, fvs.MappingEdges, fvs.Resolved)
			}
		}
		if !foundNIST {
			t.Error("nist80053/r5 not found in corpus_status")
		}
	})

	// --- search: semantic ---
	t.Run("search_semantic", func(t *testing.T) {
		out, err := core.Search(ctx, SearchInput{
			Query:     "access control account management",
			Framework: "nist80053",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Hits) == 0 {
			t.Fatal("expected hits for AC semantic query")
		}
		if out.Abstain {
			t.Error("should not abstain for in-scope query")
		}
		t.Logf("search_semantic: %d hits, top score %.5f, top cite %q",
			len(out.Hits), out.Hits[0].Score, out.Hits[0].Citation)
	})

	// --- search: citation (zero-padded norm) ---
	t.Run("search_citation", func(t *testing.T) {
		// The DB stores zero-padded norms (AC-02, not AC-2). Citation routing
		// pins on exact citation_norm match, so use the padded form.
		out, err := core.Search(ctx, SearchInput{
			Query: "AC-02",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Hits) == 0 {
			t.Fatal("expected hits for AC-02 citation")
		}
		found := false
		for _, h := range out.Hits {
			if h.CitationNorm == "AC-02" && h.Score == 1.0 {
				found = true
				break
			}
		}
		if !found {
			for i, h := range out.Hits {
				t.Logf("hit[%d]: %s score=%.5f", i, h.CitationNorm, h.Score)
			}
			t.Error("AC-02 should be pinned at score 1.0")
		}
		t.Logf("search_citation: %d hits, top score %.5f", len(out.Hits), out.Hits[0].Score)
	})

	// --- search: OOS (score floor = 0 by default, so no abstain) ---
	t.Run("search_oos", func(t *testing.T) {
		out, err := core.Search(ctx, SearchInput{
			Query: "What is the best recipe for chocolate cake?",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Hits) == 0 {
			t.Fatal("expected hits even for OOS (retriever returns something)")
		}
		t.Logf("search_oos: %d hits, top score %.5f (floor 0 = no abstain: abstain=%v)",
			len(out.Hits), out.Hits[0].Score, out.Abstain)
	})

	// --- document: AC-2 ---
	t.Run("document_ac2", func(t *testing.T) {
		out, err := core.Document(ctx, DocumentInput{
			Citation:      "AC-2",
			FrameworkCode: "nist80053",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Found {
			t.Fatal("AC-2 should be found")
		}
		if out.Control.CitationNorm != "AC-2" && out.Control.CitationNorm != "AC-02" {
			t.Errorf("expected AC-2 or AC-02, got %q", out.Control.CitationNorm)
		}
		if out.Control.Body == "" {
			t.Error("AC-2 body should not be empty (NIST is public domain)")
		}
		if len(out.Mappings) == 0 && len(out.InboundMappings) == 0 {
			t.Error("AC-2 should have mapping edges")
		}
		if len(out.VersionLineage) == 0 {
			t.Error("AC-2 should have version lineage")
		}
		t.Logf("document: %s %s/%s, %d outbound mappings, %d inbound, %d lineage rows, %d chunks",
			out.Control.CitationNorm, out.Control.FrameworkCode, out.Control.VersionLabel,
			len(out.Mappings), len(out.InboundMappings), len(out.VersionLineage), len(out.Chunks))
	})

	// --- document: CSF PR.AA-01 with inbound mappings ---
	t.Run("document_pr_aa_01", func(t *testing.T) {
		out, err := core.Document(ctx, DocumentInput{
			Citation:      "PR.AA-01",
			FrameworkCode: "nistcsf",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Found {
			t.Fatal("PR.AA-01 should be found")
		}
		if len(out.Mappings) == 0 {
			t.Error("PR.AA-01 should have outbound mappings (CSF informative references)")
		}
		t.Logf("document PR.AA-01: %d outbound mappings, %d inbound",
			len(out.Mappings), len(out.InboundMappings))
	})

	// --- document: not found ---
	t.Run("document_not_found", func(t *testing.T) {
		out, err := core.Document(ctx, DocumentInput{
			Citation: "XX-99-DOES-NOT-EXIST",
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Found {
			t.Error("XX-99 should not be found")
		}
		if len(out.Gaps) == 0 {
			t.Error("expected gap for not-found")
		}
	})

	// --- quality_gaps ---
	t.Run("quality_gaps", func(t *testing.T) {
		out, err := core.QualityGaps(ctx, QualityGapsInput{})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Categories) < 4 {
			t.Errorf("expected >=4 categories, got %d", len(out.Categories))
		}
		// We know there are unresolved mappings.
		if len(out.UnresolvedMappings) == 0 {
			t.Error("expected unresolved mappings")
		}
		if len(out.BodyQualityCaveats) < 2 {
			t.Errorf("expected >=2 body quality caveats, got %d", len(out.BodyQualityCaveats))
		}
		if len(out.EvalFloors) < 4 {
			t.Errorf("expected >=4 eval floors, got %d", len(out.EvalFloors))
		}
		t.Logf("quality_gaps: %d unresolved mapping groups, %d deferred docs, %d manifest gaps, %d caveats, %d floors",
			len(out.UnresolvedMappings), len(out.DeferredDocs), len(out.ManifestGaps),
			len(out.BodyQualityCaveats), len(out.EvalFloors))

		// Check top unresolved mapping target.
		if len(out.UnresolvedMappings) > 0 {
			top := out.UnresolvedMappings[0]
			t.Logf("top unresolved: %s/%s %s (count=%d, from=%s)",
				top.ToFrameworkCode, top.ToVersionLabel, top.ToCitationNorm, top.Count, top.FromFrameworks)
		}
	})

	// --- document with reduced projection ---
	t.Run("document_reduced_projection", func(t *testing.T) {
		reducedCore := NewCore(retriever, corpus, nil, WithProjection(ProjectionReduced))
		out, err := reducedCore.Document(ctx, DocumentInput{
			Citation:      "AC-2",
			FrameworkCode: "nist80053",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Found {
			t.Fatal("AC-2 should be found")
		}
		// Chunks should have content stripped.
		for i, ch := range out.Chunks {
			if ch.Content != "" {
				t.Errorf("chunk[%d].Content should be stripped under reduced projection", i)
			}
		}
		// Mappings should survive.
		if len(out.Mappings) == 0 && len(out.InboundMappings) == 0 {
			t.Error("mapping edges should survive reduced projection")
		}
		t.Logf("reduced document: %d chunks (content stripped), %d mappings",
			len(out.Chunks), len(out.Mappings))
	})
}

// --- Test helpers ---

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pw := os.Getenv("COMPLIARY_DATABASE_PASSWORD")
	if pw == "" {
		pw = "compliary"
	}
	dsn := fmt.Sprintf("postgres://compliary:%s@localhost:10011/compliary?sslmode=disable", pw)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("cannot connect to dev DB: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("cannot ping dev DB: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func testEmbedder(t *testing.T) ragembed.Embedder {
	t.Helper()
	home := os.Getenv("HOME")
	onnxModel := home + "/.cache/banhmi/qwen3-embedding/model_fp16.onnx"
	onnxTokenizer := home + "/.cache/banhmi/qwen3-embedding/tokenizer.json"
	onnxLib := os.Getenv("COMPLIARY_ONNX_LIB")
	if onnxLib == "" {
		onnxLib = home + "/.local/lib/libonnxruntime.so"
	}
	embedder, err := onnxembed.New(onnxembed.Config{
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
		t.Skipf("ONNX embedder not available: %v", err)
	}
	return embedder
}
