-- +goose Up
-- Drop index "idx_gold_embedding_hnsw" from table: "chunk_embedding"
DROP INDEX "gold"."idx_gold_embedding_hnsw";
