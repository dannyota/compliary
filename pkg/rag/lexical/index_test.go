package lexical

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	dbgold "danny.vn/compliary/pkg/store/gold"
)

// fakeSparseStore implements SparseQuerier in memory.
type fakeSparseStore struct {
	rows    []dbgold.ListChunksMissingSparseRow
	updated map[int64]string // id -> sparse literal
}

func newFakeSparseStore(chunks []dbgold.ListChunksMissingSparseRow) *fakeSparseStore {
	return &fakeSparseStore{
		rows:    chunks,
		updated: make(map[int64]string),
	}
}

func (s *fakeSparseStore) ListChunksMissingSparse(_ context.Context) ([]dbgold.ListChunksMissingSparseRow, error) {
	// Return only rows not yet updated (simulate the WHERE content_sparse IS NULL).
	var out []dbgold.ListChunksMissingSparseRow
	for _, r := range s.rows {
		if _, done := s.updated[r.ID]; !done {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *fakeSparseStore) UpdateChunkSparse(_ context.Context, arg dbgold.UpdateChunkSparseParams) error {
	if arg.ContentSparse == nil {
		return fmt.Errorf("content_sparse must not be nil")
	}
	s.updated[arg.ID] = *arg.ContentSparse
	return nil
}

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func ptrStr(s string) *string { return &s }

func TestIndexCorpus_writesAllChunks(t *testing.T) {
	store := newFakeSparseStore([]dbgold.ListChunksMissingSparseRow{
		{ID: 1, ContextPrefix: ptrStr("nist80053 r5"), Content: "AC-2 Account Management"},
		{ID: 2, ContextPrefix: ptrStr("nist80053 r5"), Content: "AC-2(3) Additional Authenticator"},
		{ID: 3, ContextPrefix: ptrStr("nistcsf 2.0"), Content: "PR.AA-01 Identity Management"},
	})

	written, err := IndexCorpus(context.Background(), store, 10, testLog())
	if err != nil {
		t.Fatalf("IndexCorpus: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3", written)
	}
	if len(store.updated) != 3 {
		t.Errorf("updated = %d, want 3", len(store.updated))
	}

	// Verify each sparse literal is non-empty and has the right dimension suffix.
	for id, lit := range store.updated {
		if lit == fmt.Sprintf("{}/1048576") {
			t.Errorf("chunk %d: empty sparse vector", id)
		}
		if !containsSuffix(lit, "/1048576") {
			t.Errorf("chunk %d: sparse literal missing /1048576 suffix: %s", id, lit[:min(len(lit), 40)])
		}
	}
}

func TestIndexCorpus_idempotent(t *testing.T) {
	store := newFakeSparseStore([]dbgold.ListChunksMissingSparseRow{
		{ID: 1, Content: "AC-2 Account Management"},
	})

	n1, err := IndexCorpus(context.Background(), store, 10, testLog())
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("run 1 wrote %d, want 1", n1)
	}

	// Second run: store says nothing is missing (all updated).
	n2, err := IndexCorpus(context.Background(), store, 10, testLog())
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if n2 != 0 {
		t.Errorf("run 2 wrote %d, want 0 (idempotent)", n2)
	}
}

func TestIndexCorpus_emptyCorpus(t *testing.T) {
	store := newFakeSparseStore(nil)

	n, err := IndexCorpus(context.Background(), store, 10, testLog())
	if err != nil {
		t.Fatalf("IndexCorpus: %v", err)
	}
	if n != 0 {
		t.Errorf("wrote %d, want 0 for empty corpus", n)
	}
}

// TestIndexCorpus_runWhenMissingRegardlessOfFileEligibility verifies that
// IndexCorpus processes chunks that lack sparse vectors even when called
// independently (no prior chunk-build in this invocation). This exercises
// the defect-fix contract: lexindex must run-when-missing, not only when
// new files are eligible.
func TestIndexCorpus_runWhenMissingRegardlessOfFileEligibility(t *testing.T) {
	// Simulate chunks that were built by a prior run but never got sparse vectors
	// (e.g., the prior run crashed after chunk-build but before lexindex).
	store := newFakeSparseStore([]dbgold.ListChunksMissingSparseRow{
		{ID: 100, ContextPrefix: ptrStr("nist80053 r5"), Content: "AC-1 Policy and Procedures"},
		{ID: 200, ContextPrefix: ptrStr("ciscontrols v8.1"), Content: "4.1 Establish a Secure Configuration Process"},
	})

	// No chunk-build step — go straight to IndexCorpus.
	written, err := IndexCorpus(context.Background(), store, 10, testLog())
	if err != nil {
		t.Fatalf("IndexCorpus: %v", err)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2 (should process pre-existing missing chunks)", written)
	}
}

func containsSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
