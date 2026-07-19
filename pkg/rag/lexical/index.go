package lexical

import (
	"context"
	"fmt"
	"log/slog"

	dbgold "danny.vn/compliary/pkg/store/gold"
)

// SparseQuerier is the subset of dbgold.Queries needed by IndexCorpus.
type SparseQuerier interface {
	ListChunksMissingSparse(ctx context.Context) ([]dbgold.ListChunksMissingSparseRow, error)
	UpdateChunkSparse(ctx context.Context, arg dbgold.UpdateChunkSparseParams) error
}

// IndexCorpus trains BM25 on all chunks missing sparse vectors and writes
// their document vectors into gold.chunk.content_sparse. It uses the
// sqlc-generated ListChunksMissingSparse / UpdateChunkSparse queries (no raw
// pgx) — the same pattern banhmi uses but routed through the typed store.
//
// batchSize controls how many updates are issued per transaction-less loop
// iteration; this is purely a progress-logging knob since each UPDATE is
// independent.
//
// Returns the number of chunks written.
func IndexCorpus(ctx context.Context, goldQ SparseQuerier, batchSize int, log *slog.Logger) (int, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	missing, err := goldQ.ListChunksMissingSparse(ctx)
	if err != nil {
		return 0, fmt.Errorf("list chunks missing sparse: %w", err)
	}
	if len(missing) == 0 {
		log.Info("lexindex: all chunks have sparse vectors")
		return 0, nil
	}
	log.Info("lexindex: chunks to index", "count", len(missing))

	// Build training corpus: context_prefix + content (same text used for embeddings).
	type chunk struct {
		id   int64
		text string
	}
	chunks := make([]chunk, len(missing))
	texts := make([]string, len(missing))
	for i, row := range missing {
		prefix := ""
		if row.ContextPrefix != nil {
			prefix = *row.ContextPrefix
		}
		text := row.Content
		if prefix != "" {
			text = prefix + " " + text
		}
		chunks[i] = chunk{id: row.ID, text: text}
		texts[i] = text
	}

	enc := Train(texts)
	log.Info("lexindex: trained BM25 encoder", "vocab_size", len(enc.idf), "avgdl", fmt.Sprintf("%.1f", enc.avgdl))

	written := 0
	for i, c := range chunks {
		vec := enc.DocVector(c.text)
		err := goldQ.UpdateChunkSparse(ctx, dbgold.UpdateChunkSparseParams{
			ID:            c.id,
			ContentSparse: &vec,
		})
		if err != nil {
			return written, fmt.Errorf("update chunk %d sparse: %w", c.id, err)
		}
		written++
		if written%batchSize == 0 || written == len(chunks) {
			log.Info("lexindex: progress", "written", written, "total", len(chunks))
		}
		_ = i
	}
	return written, nil
}
