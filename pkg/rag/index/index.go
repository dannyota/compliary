// Package index implements the Index pipeline stage: building gold.chunk rows
// (one per silver.control) and filling gold.chunk_embedding with dense vectors.
// The stage is idempotent — it only processes chunks that are missing embeddings
// for the configured model. Embedding can run via the local ONNX embedder or the
// Kaggle batch engine (selected by config).
package index

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	pgvector "github.com/pgvector/pgvector-go"

	"danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/embed/kagglebatch"
	dbgold "danny.vn/compliary/pkg/store/gold"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// Embedder is re-exported from the embed package for use by callers that only
// import the index package.
type Embedder = embed.Embedder

// Summary holds the result counters from an index run.
type Summary struct {
	ChunksCreated    int
	ChunksDeleted    int
	EmbeddingsUpsert int
}

// GoldQuerier is the subset of dbgold.Querier needed by the indexer.
type GoldQuerier interface {
	DeleteOrphanChunks(ctx context.Context) (int64, error)
	InsertChunk(ctx context.Context, arg dbgold.InsertChunkParams) (int64, error)
	DeleteChunksForControls(ctx context.Context, dollar_1 []int64) (int64, error)
	ListChunksMissingEmbedding(ctx context.Context, model string) ([]dbgold.ListChunksMissingEmbeddingRow, error)
	UpsertChunkEmbedding(ctx context.Context, arg dbgold.UpsertChunkEmbeddingParams) error
}

// Indexer builds gold chunks from silver controls and embeds them.
type Indexer struct {
	Embedder embed.Embedder
	Log      *slog.Logger
	// BatchSize is the number of texts to embed in one call (default 32).
	BatchSize int
}

// ReapOrphans deletes gold.chunk rows whose control_id no longer exists in
// silver.control. Should be called at the start of each index run to clean up
// after re-normalize (which rebuilds control trees with new IDs).
func (idx *Indexer) ReapOrphans(ctx context.Context, goldQ GoldQuerier) (int64, error) {
	reaped, err := goldQ.DeleteOrphanChunks(ctx)
	if err != nil {
		return 0, fmt.Errorf("reap orphan chunks: %w", err)
	}
	if reaped > 0 {
		idx.Log.Info("index: reaped orphan chunks", "count", reaped)
	}
	return reaped, nil
}

// EquivalentBody is the retrieval-enrichment payload for one control: the body
// of its `equivalent`-mapped counterpart in another framework, with the
// counterpart's true citation so the appended text is never misattributed.
type EquivalentBody struct {
	Citation  string
	Framework string
	Body      string
}

// BuildChunks creates one gold.chunk per silver.control for the given document,
// deleting any existing chunks for those controls first (idempotent rebuild).
// equivalents maps control ID → its equivalent counterpart's body (may be nil);
// when present, the counterpart's body is appended to the chunk content under a
// source label, so retrieval finds the control by its equivalent's richer text
// (27001 Annex A one-liners gain their 27002 guidance). The control's own body
// is never modified — chunks are the retrieval surface, body is the evidence.
// Returns the number of chunks created.
func (idx *Indexer) BuildChunks(ctx context.Context, doc dbsilver.SilverDocument, controls []dbsilver.SilverControl, equivalents map[int64]EquivalentBody, goldQ GoldQuerier) (int, error) {
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
		var eq *EquivalentBody
		if e, ok := equivalents[ctrl.ID]; ok {
			eq = &e
		}
		content := buildContent(ctrl, eq)

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

// EmbedMissingKaggle finds chunks missing embeddings for the given model and
// embeds them via the Kaggle batch engine. It streams input to disk and upserts
// vectors one at a time on return (bounded memory). Returns the number of
// embeddings upserted. The frameworks parameter lists the framework codes whose
// text is being sent to Kaggle — logged as a licensing warning.
func (idx *Indexer) EmbedMissingKaggle(ctx context.Context, goldQ GoldQuerier, batch *kagglebatch.BatchEmbedder, model string, dims int, frameworks []string) (int, error) {
	missing, err := goldQ.ListChunksMissingEmbedding(ctx, model)
	if err != nil {
		return 0, fmt.Errorf("list chunks missing embedding: %w", err)
	}
	if len(missing) == 0 {
		idx.Log.Info("index: all chunks have embeddings", "model", model)
		return 0, nil
	}

	// LICENSING WARNING: chunk texts are uploaded to Kaggle (operator's private
	// dataset, internal use). Log which frameworks are included.
	idx.Log.Warn("index: uploading chunk texts to Kaggle for batch embedding — licensed control text leaves this machine",
		"frameworks", frameworks, "chunks", len(missing))

	// Build the id index so onVector can upsert by chunk ID.
	ids := make([]int64, len(missing))

	n, err := batch.EmbedStream(ctx,
		func(w *kagglebatch.InputWriter) error {
			for i, chunk := range missing {
				ids[i] = chunk.ID
				text := embedText(chunk.ContextPrefix, chunk.Content)
				if err := w.Write(text); err != nil {
					return err
				}
			}
			return nil
		},
		func(index int, vec []float32) error {
			return goldQ.UpsertChunkEmbedding(ctx, dbgold.UpsertChunkEmbeddingParams{
				ChunkID:   ids[index],
				Model:     model,
				Dims:      int32(dims),
				Embedding: pgvector.NewVector(vec),
			})
		},
	)
	if err != nil {
		return 0, fmt.Errorf("kaggle batch embed: %w", err)
	}

	idx.Log.Info("index: kaggle batch embed complete", "embedded", n)
	return n, nil
}

// CountMissing returns the number of chunks missing embeddings for the given model.
func (idx *Indexer) CountMissing(ctx context.Context, goldQ GoldQuerier, model string) (int, error) {
	missing, err := goldQ.ListChunksMissingEmbedding(ctx, model)
	if err != nil {
		return 0, fmt.Errorf("count chunks missing embedding: %w", err)
	}
	return len(missing), nil
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

// buildContent builds the chunk body: citation + title + body, optionally
// followed by the equivalent counterpart's body under a label that names its
// true source — the appended text must never read as this control's own.
func buildContent(ctrl dbsilver.SilverControl, eq *EquivalentBody) string {
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
	if eq != nil && eq.Body != "" {
		b.WriteString("\n\n[equivalent ")
		b.WriteString(eq.Framework)
		b.WriteString(" ")
		b.WriteString(eq.Citation)
		b.WriteString("]\n")
		b.WriteString(eq.Body)
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
