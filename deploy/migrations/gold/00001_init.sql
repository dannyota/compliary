-- +goose Up
-- Add new schema named "gold"
CREATE SCHEMA IF NOT EXISTS "gold";
-- Create "chunk" table
CREATE TABLE "gold"."chunk" ("id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY, "control_id" bigint NOT NULL, "citation" text NOT NULL, "context_prefix" text NULL, "content" text NOT NULL, "ordinal" integer NOT NULL DEFAULT 0, "token_count" integer NULL, "content_sparse" sparsevec(1048576) NULL, PRIMARY KEY ("id"), CONSTRAINT "uq_gold_chunk" UNIQUE ("control_id", "ordinal"));
-- Create index "idx_gold_chunk_control" to table: "chunk"
CREATE INDEX "idx_gold_chunk_control" ON "gold"."chunk" ("control_id");
-- Create "chunk_embedding" table
CREATE TABLE "gold"."chunk_embedding" ("id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY, "chunk_id" bigint NOT NULL, "model" text NOT NULL, "dims" integer NOT NULL, "embedding" vector(1024) NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "uq_gold_chunk_embedding" UNIQUE ("chunk_id", "model", "dims"), CONSTRAINT "fk_gold_embedding_chunk" FOREIGN KEY ("chunk_id") REFERENCES "gold"."chunk" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "idx_gold_embedding_chunk" to table: "chunk_embedding"
CREATE INDEX "idx_gold_embedding_chunk" ON "gold"."chunk_embedding" ("chunk_id");
-- Create index "idx_gold_embedding_hnsw" to table: "chunk_embedding"
CREATE INDEX "idx_gold_embedding_hnsw" ON "gold"."chunk_embedding" USING hnsw ("embedding" vector_cosine_ops);
