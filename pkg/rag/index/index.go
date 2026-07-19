// Package index implements the Index pipeline stage: building gold.chunk rows
// (one per silver.control) and filling gold.chunk_embedding with dense vectors
// from the local ONNX embedder. The stage is idempotent — it only processes
// chunks that are missing embeddings for the configured model.
package index

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	pgvector "github.com/pgvector/pgvector-go"

	"danny.vn/compliary/pkg/rag/embed"
	dbgold "danny.vn/compliary/pkg/store/gold"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// Summary holds the result counters from an index run.
type Summary struct {
	ChunksCreated    int
	ChunksDeleted    int
	EmbeddingsUpsert int
}

// GoldQuerier is the subset of dbgold.Querier needed by the indexer.
type GoldQuerier interface {
	InsertChunk(ctx context.Context, arg dbgold.InsertChunkParams) (int64, error)
	DeleteChunksForControls(ctx context.Context, dollar_1 []int64) (int64, error)
	ListChunksMissingEmbedding(ctx context.Context, model string) ([]dbgold.ListChunksMissingEmbeddingRow, error)
	UpsertChunkEmbedding(ctx context.Context, arg dbgold.UpsertChunkEmbeddingParams) error
}

// SilverQuerier is the subset of dbsilver.Querier needed by the indexer.
type SilverQuerier interface {
	ListDocuments(ctx context.Context) ([]dbsilver.SilverDocument, error)
	ListControlsForDocument(ctx context.Context, documentID int64) ([]dbsilver.SilverControl, error)
}

// Indexer builds gold chunks from silver controls and embeds them.
type Indexer struct {
	Embedder embed.Embedder
	Log      *slog.Logger
	// BatchSize is the number of texts to embed in one call (default 32).
	BatchSize int
}

// BuildChunks creates one gold.chunk per silver.control for the given document,
// deleting any existing chunks for those controls first (idempotent rebuild).
// Returns the number of chunks created.
func (idx *Indexer) BuildChunks(ctx context.Context, doc dbsilver.SilverDocument, controls []dbsilver.SilverControl, goldQ GoldQuerier) (int, error) {
	if len(controls) == 0 {
		return 0, nil
	}

	// Build an ID-to-control index for ancestor-path lookups.
	byID := make(map[int64]*dbsilver.SilverControl, len(controls))
	for i := range controls {
		byID[controls[i].ID] = &controls[i]
	}

	// Delete existing chunks for these controls (idempotent rebuild).
	ids := make([]int64, len(controls))
	for i, c := range controls {
		ids[i] = c.ID
	}
	deleted, err := goldQ.DeleteChunksForControls(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("delete existing chunks: %w", err)
	}
	if deleted > 0 {
		idx.Log.Info("index: deleted stale chunks", "document", doc.DocKey, "deleted", deleted)
	}

	var created int
	for _, ctrl := range controls {
		prefix := buildContextPrefix(doc, ctrl, byID)
		content := buildContent(ctrl)

		_, err := goldQ.InsertChunk(ctx, dbgold.InsertChunkParams{
			ControlID:     ctrl.ID,
			Citation:      ctrl.Citation,
			ContextPrefix: &prefix,
			Content:       content,
			Ordinal:       0,
			TokenCount:    nil, // filled by embedder if needed
		})
		if err != nil {
			return created, fmt.Errorf("insert chunk for %s: %w", ctrl.CitationNorm, err)
		}
		created++
	}

	return created, nil
}

// EmbedMissing finds chunks missing embeddings for the configured model and
// embeds them in batches. Returns the number of embeddings upserted.
func (idx *Indexer) EmbedMissing(ctx context.Context, goldQ GoldQuerier) (int, error) {
	if idx.Embedder == nil {
		return 0, fmt.Errorf("index: no embedder configured")
	}

	missing, err := goldQ.ListChunksMissingEmbedding(ctx, idx.Embedder.Model())
	if err != nil {
		return 0, fmt.Errorf("list chunks missing embedding: %w", err)
	}
	if len(missing) == 0 {
		idx.Log.Info("index: all chunks have embeddings", "model", idx.Embedder.Model())
		return 0, nil
	}
	idx.Log.Info("index: chunks to embed", "count", len(missing), "model", idx.Embedder.Model())

	batchSize := idx.BatchSize
	if batchSize <= 0 {
		batchSize = 32
	}

	var upserted int
	for i := 0; i < len(missing); i += batchSize {
		end := i + batchSize
		if end > len(missing) {
			end = len(missing)
		}
		batch := missing[i:end]

		texts := make([]string, len(batch))
		for j, chunk := range batch {
			texts[j] = embedText(chunk.ContextPrefix, chunk.Content)
		}

		vecs, err := idx.Embedder.Embed(ctx, texts)
		if err != nil {
			return upserted, fmt.Errorf("embed batch [%d:%d]: %w", i, end, err)
		}

		for j, chunk := range batch {
			err := goldQ.UpsertChunkEmbedding(ctx, dbgold.UpsertChunkEmbeddingParams{
				ChunkID:   chunk.ID,
				Model:     idx.Embedder.Model(),
				Dims:      int32(idx.Embedder.Dims()),
				Embedding: pgvector.NewVector(vecs[j]),
			})
			if err != nil {
				return upserted, fmt.Errorf("upsert embedding for chunk %d: %w", chunk.ID, err)
			}
			upserted++
		}

		idx.Log.Info("index: embedded batch", "batch", i/batchSize+1, "upserted", len(batch))
	}

	return upserted, nil
}

// buildContextPrefix builds the contextual retrieval header:
// "{framework_name} {version_label} > {ancestor citations...} > {title}"
// Uses the control's TITLE (safe/paraphrased), NEVER title_original.
func buildContextPrefix(doc dbsilver.SilverDocument, ctrl dbsilver.SilverControl, byID map[int64]*dbsilver.SilverControl) string {
	var parts []string

	// Framework name + version.
	parts = append(parts, doc.FrameworkCode+" "+doc.VersionLabel)

	// Ancestor citation path (walk up parent_control_id).
	ancestors := ancestorCitations(ctrl, byID)
	parts = append(parts, ancestors...)

	// Control's own title (safe, paraphrased).
	if ctrl.Title != "" {
		parts = append(parts, ctrl.Title)
	}

	return strings.Join(parts, " > ")
}

// ancestorCitations returns the citation path from root to the control's parent,
// in root-first order. Does not include the control itself.
func ancestorCitations(ctrl dbsilver.SilverControl, byID map[int64]*dbsilver.SilverControl) []string {
	var chain []string
	cur := ctrl.ParentControlID
	for cur != nil {
		parent, ok := byID[*cur]
		if !ok {
			break
		}
		chain = append(chain, parent.Citation)
		cur = parent.ParentControlID
	}
	// Reverse to root-first order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// buildContent builds the chunk body: citation + title + body.
func buildContent(ctrl dbsilver.SilverControl) string {
	var b strings.Builder
	b.WriteString(ctrl.Citation)
	if ctrl.Title != "" {
		b.WriteString(" ")
		b.WriteString(ctrl.Title)
	}
	if ctrl.Body != nil && *ctrl.Body != "" {
		b.WriteString("\n")
		b.WriteString(*ctrl.Body)
	}
	return b.String()
}

// embedText concatenates context_prefix and content for embedding.
// Documents are embedded as-is (no query prefix).
func embedText(prefix *string, content string) string {
	if prefix != nil && *prefix != "" {
		return *prefix + "\n" + content
	}
	return content
}
