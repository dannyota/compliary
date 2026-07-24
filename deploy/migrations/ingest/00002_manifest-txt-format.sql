-- +goose Up
-- Modify "manifest_file" table
ALTER TABLE "ingest"."manifest_file" DROP CONSTRAINT "chk_ingest_manifest_file_format", ADD CONSTRAINT "chk_ingest_manifest_file_format" CHECK (file_format = ANY (ARRAY['oscal-json'::text, 'xlsx'::text, 'pdf'::text, 'txt'::text]));
