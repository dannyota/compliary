package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	ragembed "danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/embed/onnxembed"
	"danny.vn/compliary/pkg/rag/retrieve"
	dbsilver "danny.vn/compliary/pkg/store/silver"
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

// TestIntegration_CuratedMappingDrift exercises the quality_gaps
// curated-mapping drift surface against the real dev corpus. It needs the DB
// only (no embedder), so it runs without the onnx build tag.
//
// Why this gap exists: re-normalizing a framework deletes and recreates its
// silver.control rows, which cascade-deletes the silver.control_mapping edges
// pointing at them. mapedges is not in cmd/pipeline's default stage list, so a
// normalize run silently drops curated edges until mapedges is re-run.
func TestIntegration_CuratedMappingDrift(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	corpus := DBCorpus(pool)

	// A seeded-but-never-mapedged DB is a legitimate intermediate state for a
	// self-deployer; drift is the correct answer there, so skip rather than fail.
	var edges int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM silver.control_mapping`).Scan(&edges); err != nil {
		t.Fatal(err)
	}
	if edges == 0 {
		t.Skip("no mapping edges: run the mapedges stage before this test")
	}

	// --- healthy corpus: no drift, no drift note ---
	out, err := corpus.QualityGaps(ctx, QualityGapsInput{Category: "curated_mappings"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.CuratedMappingGaps) != 0 {
		t.Errorf("healthy corpus: expected no curated-mapping drift, got %d rows (first: %+v)",
			len(out.CuratedMappingGaps), out.CuratedMappingGaps[0])
	}
	for _, n := range out.Notes {
		if strings.Contains(n, "curated mapping drift") {
			t.Errorf("healthy corpus: unexpected drift note %q", n)
		}
	}
	t.Logf("healthy corpus: 0 curated-mapping drift rows")

	// --- drift case: delete a framework's operator edges inside a rolled-back
	// transaction and run the same query the tool runs. The tool reads through
	// the pool, so the drift branch is exercised via the shared query const —
	// the dev DB is never left in a broken state.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback failed — dev DB may be left with dropped edges: %v", err)
		}
	}()

	tag, err := tx.Exec(ctx, `
DELETE FROM silver.control_mapping cm
USING silver.control sc, silver.document d
WHERE sc.id = cm.from_control_id
  AND d.id = sc.document_id
  AND d.framework_code = 'tcvn11930'
  AND cm.mapping_source_code = 'operator'`)
	if err != nil {
		t.Fatal(err)
	}
	deleted := tag.RowsAffected()
	if deleted == 0 {
		t.Skip("no tcvn11930 operator edges in this corpus — nothing to simulate")
	}

	rows, err := tx.Query(ctx, curatedMappingDriftQuery, maxGapLimit)
	if err != nil {
		t.Fatal(err)
	}
	var got, total int64
	var reason string
	for rows.Next() {
		var g CuratedMappingGap
		if err := rows.Scan(&g.FromFrameworkCode, &g.FromVersionLabel, &g.FromCitationNorm,
			&g.ToFrameworkCode, &g.ToVersionLabel, &g.ToCitationNorm,
			&g.MappingSource, &g.Reason, &total); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if g.FromFrameworkCode != "tcvn11930" {
			t.Errorf("unexpected drift row for %s (only tcvn11930 edges were dropped)", g.FromFrameworkCode)
		}
		reason = g.Reason
		got++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if got != deleted || total != deleted {
		t.Errorf("drift rows: got %d (total %d), want %d", got, total, deleted)
	}
	if !strings.Contains(reason, "mapedges") {
		t.Errorf("drift reason should name the mapedges stage, got %q", reason)
	}
	if note := curatedDriftNote(total); !strings.Contains(note, "-stage mapedges") {
		t.Errorf("drift note should name the remediation command, got %q", note)
	}
	t.Logf("simulated drift: %d dropped edges → %d drift rows, reason %q", deleted, got, reason)
}

// TestIntegration_StaleMapEdges exercises the mapedges staleness marker against
// the real dev corpus. It is the derived-edge counterpart of the curated-drift
// test above: curated drift can only see the 39 seeded operator edges, while the
// marker sees any document whose outbound edges were cascade-deleted.
//
// The simulation runs the shipped DeleteControlsForDocument query inside a
// rolled-back transaction, so it proves the real statement's data-modifying CTE
// NULLs silver.document.mapedges_at — the dev DB is never left damaged.
func TestIntegration_StaleMapEdges(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	corpus := DBCorpus(pool)

	var fresh int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM silver.document WHERE mapedges_at IS NOT NULL`).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if fresh == 0 {
		t.Skip("no document has a mapedges marker: run the mapedges stage before this test")
	}

	// --- healthy corpus: no stale documents, no stale note ---
	out, err := corpus.QualityGaps(ctx, QualityGapsInput{Category: "stale_mapedges"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.StaleMapEdges) != 0 {
		t.Errorf("healthy corpus: expected no stale-mapedges rows, got %d (first: %+v)",
			len(out.StaleMapEdges), out.StaleMapEdges[0])
	}
	for _, n := range out.Notes {
		if strings.Contains(n, "stale mapping edges") {
			t.Errorf("healthy corpus: unexpected stale note %q", n)
		}
	}
	t.Logf("healthy corpus: %d documents marked fresh, 0 stale", fresh)

	// --- stale case: re-normalize one document inside a rolled-back tx ---
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback failed — dev DB may be left with dropped controls: %v", err)
		}
	}()

	var docID int64
	var docKey string
	err = tx.QueryRow(ctx, `
SELECT d.id, d.doc_key
FROM silver.document d
JOIN silver.control sc ON sc.document_id = d.id
JOIN silver.control_mapping cm ON cm.from_control_id = sc.id
WHERE d.mapedges_at IS NOT NULL
GROUP BY d.id, d.doc_key
ORDER BY count(cm.id)
LIMIT 1`).Scan(&docID, &docKey)
	if err != nil {
		t.Skipf("no document with outbound edges to simulate against: %v", err)
	}

	// The shipped query — not a hand-written copy — so the CTE is under test.
	silverQ := dbsilver.New(tx)
	edgesBefore, err := silverQ.CountMappingsForDocument(ctx, docID)
	if err != nil {
		t.Fatal(err)
	}
	if edgesBefore == 0 {
		t.Skip("selected document has no outbound edges — nothing to lose")
	}
	if _, err := silverQ.DeleteControlsForDocument(ctx, docID); err != nil {
		t.Fatal(err)
	}

	edgesAfter, err := silverQ.CountMappingsForDocument(ctx, docID)
	if err != nil {
		t.Fatal(err)
	}
	if edgesAfter != 0 {
		t.Errorf("edges after cascade: got %d, want 0", edgesAfter)
	}

	rows, err := tx.Query(ctx, staleMapEdgesQuery, maxGapLimit)
	if err != nil {
		t.Fatal(err)
	}
	var got, total int64
	var reason string
	for rows.Next() {
		var g StaleMapEdgesGap
		if err := rows.Scan(&g.FrameworkCode, &g.VersionLabel, &g.DocRole, &g.Qualifier,
			&g.Controls, &g.OutboundEdges, &g.Reason, &total); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if !strings.HasPrefix(docKey, g.FrameworkCode+"|"+g.VersionLabel+"|") {
			t.Errorf("unexpected stale row for %s/%s (only %s was re-normalized)",
				g.FrameworkCode, g.VersionLabel, docKey)
		}
		reason = g.Reason
		got++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if got != 1 || total != 1 {
		t.Errorf("stale rows: got %d (total %d), want 1", got, total)
	}
	if !strings.Contains(reason, "re-normalized") {
		t.Errorf("stale reason should name the re-normalize cause, got %q", reason)
	}
	if note := staleMapEdgesNote(total); !strings.Contains(note, "-stage mapedges") {
		t.Errorf("stale note should name the remediation command, got %q", note)
	}
	t.Logf("simulated re-normalize of %s: %d edges cascade-deleted → marker NULL, reason %q",
		docKey, edgesBefore, reason)
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
