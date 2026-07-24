-- +goose Up
-- Modify "source_file" table
ALTER TABLE "bronze"."source_file" DROP CONSTRAINT "chk_bronze_source_file_format", ADD CONSTRAINT "chk_bronze_source_file_format" CHECK (file_format = ANY (ARRAY['oscal-json'::text, 'xlsx'::text, 'pdf'::text, 'txt'::text]));
