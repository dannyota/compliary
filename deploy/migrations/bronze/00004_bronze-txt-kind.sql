-- +goose Up
-- Modify "raw_extract" table
ALTER TABLE "bronze"."raw_extract" DROP CONSTRAINT "chk_bronze_raw_extract_kind", ADD CONSTRAINT "chk_bronze_raw_extract_kind" CHECK (kind = ANY (ARRAY['text-markdown'::text, 'oscal-catalog-json'::text, 'workbook-rows-json'::text, 'pdf-pages-json'::text, 'txt-pages-json'::text]));
