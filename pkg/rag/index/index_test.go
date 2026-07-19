package index

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	dbgold "danny.vn/compliary/pkg/store/gold"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// --- Fakes ---

// fakeEmbedder returns synthetic vectors: each element is float32(textIndex+1).
type fakeEmbedder struct {
	dims  int
	model string
	calls int
}

func (f *fakeEmbedder) Model() string { return f.model }
func (f *fakeEmbedder) Dims() int     { return f.dims }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, f.dims)
		for j := range vec {
			vec[j] = float32(i + 1)
		}
		out[i] = vec
	}
	return out, nil
}

// fakeGoldStore implements GoldQuerier in memory.
type fakeGoldStore struct {
	chunks     map[int64]dbgold.GoldChunk // id -> chunk
	embeddings map[int64]dbgold.UpsertChunkEmbeddingParams
	nextID     int64
}

func newFakeGoldStore() *fakeGoldStore {
	return &fakeGoldStore{
		chunks:     make(map[int64]dbgold.GoldChunk),
		embeddings: make(map[int64]dbgold.UpsertChunkEmbeddingParams),
		nextID:     1,
	}
}

func (s *fakeGoldStore) InsertChunk(_ context.Context, arg dbgold.InsertChunkParams) (int64, error) {
	id := s.nextID
	s.nextID++
	s.chunks[id] = dbgold.GoldChunk{
		ID:            id,
		ControlID:     arg.ControlID,
		Citation:      arg.Citation,
		ContextPrefix: arg.ContextPrefix,
		Content:       arg.Content,
		Ordinal:       arg.Ordinal,
		TokenCount:    arg.TokenCount,
	}
	return id, nil
}

func (s *fakeGoldStore) DeleteChunksForControls(_ context.Context, ids []int64) (int64, error) {
	idSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var deleted int64
	for chunkID, chunk := range s.chunks {
		if idSet[chunk.ControlID] {
			delete(s.chunks, chunkID)
			deleted++
		}
	}
	return deleted, nil
}

func (s *fakeGoldStore) ListChunksMissingEmbedding(_ context.Context, model string) ([]dbgold.ListChunksMissingEmbeddingRow, error) {
	var result []dbgold.ListChunksMissingEmbeddingRow
	for _, chunk := range s.chunks {
		if _, ok := s.embeddings[chunk.ID]; !ok {
			result = append(result, dbgold.ListChunksMissingEmbeddingRow{
				ID:            chunk.ID,
				ContextPrefix: chunk.ContextPrefix,
				Content:       chunk.Content,
			})
		}
	}
	return result, nil
}

func (s *fakeGoldStore) UpsertChunkEmbedding(_ context.Context, arg dbgold.UpsertChunkEmbeddingParams) error {
	s.embeddings[arg.ChunkID] = arg
	return nil
}

// --- Test data: a tiny synthetic control tree ---

func ptrStr(s string) *string { return &s }
func ptrI64(n int64) *int64   { return &n }

// makeTestTree builds a synthetic tree:
//
//	doc: nist80053 r5, role=main
//	  family: AC (Access Control), id=1
//	    control: AC-1, id=2
//	      enhancement: AC-1(1), id=3
//	    control: AC-2, id=4
func makeTestTree() (dbsilver.SilverDocument, []dbsilver.SilverControl) {
	doc := dbsilver.SilverDocument{
		ID:            100,
		DocKey:        "nist80053-r5-main",
		FrameworkCode: "nist80053",
		VersionLabel:  "r5",
		DocRole:       "main",
		Title:         "NIST SP 800-53 Rev 5",
	}
	controls := []dbsilver.SilverControl{
		{ID: 1, DocumentID: 100, Citation: "AC", CitationNorm: "ac", Kind: "family", Status: "active", Title: "Access Control", Ordinal: 1},
		{ID: 2, DocumentID: 100, ParentControlID: ptrI64(1), Citation: "AC-1", CitationNorm: "ac-1", Kind: "control", Status: "active", Title: "Policy and Procedures", Body: ptrStr("Develop and maintain access control policy."), Ordinal: 2},
		{ID: 3, DocumentID: 100, ParentControlID: ptrI64(2), Citation: "AC-1(1)", CitationNorm: "ac-1(1)", Kind: "enhancement", Status: "active", Title: "Automated Policy Management", Body: ptrStr("Implement automated support for policy management."), Ordinal: 3},
		{ID: 4, DocumentID: 100, ParentControlID: ptrI64(1), Citation: "AC-2", CitationNorm: "ac-2", Kind: "control", Status: "active", Title: "Account Management", Body: ptrStr("Manage information system accounts."), Ordinal: 4},
	}
	return doc, controls
}

// --- Tests ---

func TestBuildChunks_createsOnePerControl(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	idx := &Indexer{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	created, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks: %v", err)
	}
	if created != len(controls) {
		t.Errorf("created = %d, want %d", created, len(controls))
	}
	if len(store.chunks) != len(controls) {
		t.Errorf("store has %d chunks, want %d", len(store.chunks), len(controls))
	}
}

func TestBuildChunks_contextPrefix(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	idx := &Indexer{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	_, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks: %v", err)
	}

	tests := []struct {
		controlID int64
		wantParts []string // substrings that must appear in context_prefix
	}{
		// Family (root): just framework + version + title.
		{1, []string{"nist80053 r5", "Access Control"}},
		// Control AC-1: framework + version > family citation > title.
		{2, []string{"nist80053 r5", "AC", "Policy and Procedures"}},
		// Enhancement AC-1(1): framework + version > AC > AC-1 > title.
		{3, []string{"nist80053 r5", "AC", "AC-1", "Automated Policy Management"}},
		// Control AC-2: framework + version > AC > title.
		{4, []string{"nist80053 r5", "AC", "Account Management"}},
	}

	for _, tt := range tests {
		var found *dbgold.GoldChunk
		for _, c := range store.chunks {
			if c.ControlID == tt.controlID {
				found = &c
				break
			}
		}
		if found == nil {
			t.Errorf("no chunk for control_id=%d", tt.controlID)
			continue
		}
		prefix := ""
		if found.ContextPrefix != nil {
			prefix = *found.ContextPrefix
		}
		for _, part := range tt.wantParts {
			if !strings.Contains(prefix, part) {
				t.Errorf("control_id=%d: context_prefix=%q missing %q", tt.controlID, prefix, part)
			}
		}
	}
}

func TestBuildChunks_contentFormat(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	idx := &Indexer{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	_, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks: %v", err)
	}

	// AC-1 should have: "AC-1 Policy and Procedures\nDevelop and maintain access control policy."
	for _, c := range store.chunks {
		if c.ControlID == 2 {
			if !strings.HasPrefix(c.Content, "AC-1 Policy and Procedures") {
				t.Errorf("content should start with citation+title, got %q", c.Content[:50])
			}
			if !strings.Contains(c.Content, "Develop and maintain access control policy.") {
				t.Errorf("content missing body text")
			}
			return
		}
	}
	t.Error("no chunk found for control_id=2")
}

func TestBuildChunks_neverTitleOriginal(t *testing.T) {
	doc, controls := makeTestTree()
	// Set title_original on a control; verify it never appears in chunks.
	controls[1].TitleOriginal = ptrStr("SENSITIVE ORIGINAL TITLE")
	store := newFakeGoldStore()
	idx := &Indexer{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	_, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks: %v", err)
	}

	for _, c := range store.chunks {
		if c.ControlID == 2 {
			if strings.Contains(c.Content, "SENSITIVE ORIGINAL TITLE") {
				t.Error("content must never contain title_original")
			}
			if c.ContextPrefix != nil && strings.Contains(*c.ContextPrefix, "SENSITIVE ORIGINAL TITLE") {
				t.Error("context_prefix must never contain title_original")
			}
			return
		}
	}
}

func TestBuildChunks_idempotent(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	idx := &Indexer{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}

	// First run.
	created1, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks run 1: %v", err)
	}
	// Second run: should delete and recreate.
	created2, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks run 2: %v", err)
	}
	if created1 != created2 {
		t.Errorf("run1 created %d, run2 created %d; expected same", created1, created2)
	}
	if len(store.chunks) != len(controls) {
		t.Errorf("store has %d chunks after 2 runs, want %d", len(store.chunks), len(controls))
	}
}

func TestEmbedMissing_embedsAllChunks(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	emb := &fakeEmbedder{dims: 4, model: "test-model"}
	idx := &Indexer{
		Embedder:  emb,
		Log:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		BatchSize: 2,
	}

	_, err := idx.BuildChunks(context.Background(), doc, controls, store)
	if err != nil {
		t.Fatalf("BuildChunks: %v", err)
	}

	upserted, err := idx.EmbedMissing(context.Background(), store)
	if err != nil {
		t.Fatalf("EmbedMissing: %v", err)
	}
	if upserted != len(controls) {
		t.Errorf("upserted = %d, want %d", upserted, len(controls))
	}
	if len(store.embeddings) != len(controls) {
		t.Errorf("store has %d embeddings, want %d", len(store.embeddings), len(controls))
	}

	// Verify dimensions.
	for chunkID, emb := range store.embeddings {
		if emb.Dims != 4 {
			t.Errorf("chunk %d: dims = %d, want 4", chunkID, emb.Dims)
		}
	}
}

func TestEmbedMissing_idempotent(t *testing.T) {
	doc, controls := makeTestTree()
	store := newFakeGoldStore()
	emb := &fakeEmbedder{dims: 4, model: "test-model"}
	idx := &Indexer{
		Embedder:  emb,
		Log:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		BatchSize: 2,
	}

	_, _ = idx.BuildChunks(context.Background(), doc, controls, store)

	// First embed.
	n1, err := idx.EmbedMissing(context.Background(), store)
	if err != nil {
		t.Fatalf("EmbedMissing run 1: %v", err)
	}
	// Second embed: nothing missing.
	n2, err := idx.EmbedMissing(context.Background(), store)
	if err != nil {
		t.Fatalf("EmbedMissing run 2: %v", err)
	}
	if n1 == 0 {
		t.Error("first EmbedMissing should have embedded something")
	}
	if n2 != 0 {
		t.Errorf("second EmbedMissing embedded %d, want 0", n2)
	}
}

func TestEmbedMissing_batchBoundary(t *testing.T) {
	// 5 controls with batch size 2 = 3 batches (2+2+1).
	doc := dbsilver.SilverDocument{
		ID: 200, DocKey: "test-batch", FrameworkCode: "test", VersionLabel: "1.0",
		DocRole: "main", Title: "Batch Test",
	}
	var controls []dbsilver.SilverControl
	for i := 1; i <= 5; i++ {
		controls = append(controls, dbsilver.SilverControl{
			ID: int64(i), DocumentID: 200, Citation: fmt.Sprintf("C-%d", i),
			CitationNorm: fmt.Sprintf("c-%d", i), Kind: "control", Status: "active",
			Title: fmt.Sprintf("Control %d", i), Ordinal: int32(i),
		})
	}

	store := newFakeGoldStore()
	emb := &fakeEmbedder{dims: 4, model: "test-model"}
	idx := &Indexer{Embedder: emb, Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})), BatchSize: 2}

	_, _ = idx.BuildChunks(context.Background(), doc, controls, store)
	upserted, err := idx.EmbedMissing(context.Background(), store)
	if err != nil {
		t.Fatalf("EmbedMissing: %v", err)
	}
	if upserted != 5 {
		t.Errorf("upserted = %d, want 5", upserted)
	}
	// 3 batches = 3 embed calls.
	if emb.calls != 3 {
		t.Errorf("embed calls = %d, want 3", emb.calls)
	}
}

func TestBuildContextPrefix_noParent(t *testing.T) {
	doc := dbsilver.SilverDocument{FrameworkCode: "ciscontrols", VersionLabel: "v8.1"}
	ctrl := dbsilver.SilverControl{ID: 1, Citation: "4.1", Title: "Establish a Secure Configuration Process"}
	byID := map[int64]*dbsilver.SilverControl{1: &ctrl}

	prefix := buildContextPrefix(doc, ctrl, byID)
	want := "ciscontrols v8.1 > Establish a Secure Configuration Process"
	if prefix != want {
		t.Errorf("prefix = %q, want %q", prefix, want)
	}
}

func TestAncestorCitations_deepNesting(t *testing.T) {
	root := dbsilver.SilverControl{ID: 1, Citation: "Root"}
	mid := dbsilver.SilverControl{ID: 2, ParentControlID: ptrI64(1), Citation: "Mid"}
	leaf := dbsilver.SilverControl{ID: 3, ParentControlID: ptrI64(2), Citation: "Leaf"}
	byID := map[int64]*dbsilver.SilverControl{1: &root, 2: &mid, 3: &leaf}

	ancestors := ancestorCitations(leaf, byID)
	if len(ancestors) != 2 || ancestors[0] != "Root" || ancestors[1] != "Mid" {
		t.Errorf("ancestors = %v, want [Root Mid]", ancestors)
	}
}

func TestEmbedText_withAndWithoutPrefix(t *testing.T) {
	content := "AC-1 Policy and Procedures"

	// With prefix.
	prefix := "nist80053 r5 > AC > Policy and Procedures"
	got := embedText(&prefix, content)
	if !strings.HasPrefix(got, prefix) || !strings.Contains(got, content) {
		t.Errorf("embedText with prefix: %q", got)
	}

	// Without prefix.
	got2 := embedText(nil, content)
	if got2 != content {
		t.Errorf("embedText without prefix = %q, want %q", got2, content)
	}
}

func TestBuildContent_noBody(t *testing.T) {
	ctrl := dbsilver.SilverControl{Citation: "AC", Title: "Access Control"}
	content := buildContent(ctrl)
	if content != "AC Access Control" {
		t.Errorf("content = %q, want %q", content, "AC Access Control")
	}
}

func TestBuildContent_withBody(t *testing.T) {
	ctrl := dbsilver.SilverControl{Citation: "AC-1", Title: "Policy", Body: ptrStr("The body.")}
	content := buildContent(ctrl)
	if content != "AC-1 Policy\nThe body." {
		t.Errorf("content = %q", content)
	}
}
