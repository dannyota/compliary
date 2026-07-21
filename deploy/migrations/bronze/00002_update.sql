-- +goose Up
-- Create index "idx_bronze_source_file_sha256" to table: "source_file"
CREATE INDEX "idx_bronze_source_file_sha256" ON "bronze"."source_file" ("sha256");
