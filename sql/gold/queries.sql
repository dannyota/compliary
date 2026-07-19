-- name: DeleteChunksForControls :execrows
DELETE FROM gold.chunk WHERE control_id = ANY($1::bigint[]);

-- name: InsertChunk :one
INSERT INTO gold.chunk (control_id, citation, context_prefix, content, ordinal, token_count)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: ListChunksForControl :many
SELECT * FROM gold.chunk WHERE control_id = $1 ORDER BY ordinal;

-- name: CountChunks :one
SELECT count(*) FROM gold.chunk;

-- name: ListChunksMissingEmbedding :many
SELECT c.id, c.context_prefix, c.content FROM gold.chunk c
WHERE NOT EXISTS (
    SELECT 1 FROM gold.chunk_embedding e WHERE e.chunk_id = c.id AND e.model = $1
)
ORDER BY c.id;

-- name: UpsertChunkEmbedding :exec
INSERT INTO gold.chunk_embedding (chunk_id, model, dims, embedding)
VALUES ($1, $2, $3, $4)
ON CONFLICT (chunk_id, model, dims) DO UPDATE SET embedding = EXCLUDED.embedding;

-- name: ListChunksMissingSparse :many
SELECT id, context_prefix, content FROM gold.chunk WHERE content_sparse IS NULL ORDER BY id;

-- name: UpdateChunkSparse :exec
UPDATE gold.chunk SET content_sparse = $2 WHERE id = $1;
