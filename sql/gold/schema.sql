CREATE EXTENSION IF NOT EXISTS vector;

CREATE SCHEMA IF NOT EXISTS gold;

-- gold is the RAG layer: control-level chunks + dense and sparse vectors.

-- gold.chunk: one chunk per control today; ordinal is reserved for deep
-- controls (e.g. COBIT objectives) so no migration is needed later. Whether
-- sub-control kinds (points of focus, practices) chunk individually or fold
-- into their parent's chunk is a per-framework parser decision settled by eval.
-- control_id is a business key into silver.control (no cross-schema FK).
-- citation is the display cite ("ISO/IEC 27001:2022 A.5.1"); context_prefix is
-- the contextual-retrieval header (framework + version + family/clause path +
-- paraphrased title only — never title_original). content_sparse is the BM25
-- sparse vector (pgvector sparsevec, 2^20 hashing-trick term space), NULL until
-- the LexIndex stage builds it.
CREATE TABLE gold.chunk (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    control_id     BIGINT NOT NULL,
    citation       TEXT NOT NULL,
    context_prefix TEXT,
    content        TEXT NOT NULL,
    ordinal        INTEGER NOT NULL DEFAULT 0,
    token_count    INTEGER,
    content_sparse sparsevec(1048576),
    CONSTRAINT uq_gold_chunk UNIQUE (control_id, ordinal)
);

CREATE INDEX idx_gold_chunk_control ON gold.chunk (control_id);

-- gold.chunk_embedding: one embedding per (chunk, model, dims) so multiple
-- embedders coexist and re-embedding is non-destructive. Qwen3-Embedding-0.6B,
-- 1024 dims, exact cosine scan (no HNSW — re-evaluate at 10k+ chunks).
CREATE TABLE gold.chunk_embedding (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chunk_id  BIGINT NOT NULL,
    model     TEXT NOT NULL,
    dims      INTEGER NOT NULL,
    embedding vector(1024) NOT NULL,
    CONSTRAINT fk_gold_embedding_chunk FOREIGN KEY (chunk_id)
        REFERENCES gold.chunk (id) ON DELETE CASCADE,
    CONSTRAINT uq_gold_chunk_embedding UNIQUE (chunk_id, model, dims)
);

CREATE INDEX idx_gold_embedding_chunk ON gold.chunk_embedding (chunk_id);
