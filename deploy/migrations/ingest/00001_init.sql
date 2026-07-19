-- +goose Up
-- Add new schema named "ingest"
CREATE SCHEMA IF NOT EXISTS "ingest";
-- Create "manifest_file" table
CREATE TABLE "ingest"."manifest_file" ("id" bigint NOT NULL GENERATED ALWAYS AS IDENTITY, "rel_path" text NOT NULL, "sha256" text NOT NULL, "size_bytes" bigint NOT NULL DEFAULT 0, "framework_code" text NULL, "version_label" text NULL, "doc_role" text NULL, "qualifier" text NOT NULL DEFAULT '', "file_format" text NULL, "status" text NOT NULL DEFAULT 'active', "ignored" boolean NOT NULL DEFAULT false, "ignore_reason" text NOT NULL DEFAULT '', "extracted_at" timestamptz NULL, "normalized_at" timestamptz NULL, "indexed_at" timestamptz NULL, "stage_error" text NOT NULL DEFAULT '', "first_seen_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), PRIMARY KEY ("id"), CONSTRAINT "uq_ingest_manifest_file" UNIQUE ("rel_path"), CONSTRAINT "chk_ingest_manifest_file_format" CHECK (file_format = ANY (ARRAY['oscal-json'::text, 'xlsx'::text, 'pdf'::text])), CONSTRAINT "chk_ingest_manifest_file_role" CHECK (doc_role = ANY (ARRAY['main'::text, 'amendment'::text, 'companion-workbook'::text, 'changelog'::text, 'guide'::text])), CONSTRAINT "chk_ingest_manifest_file_status" CHECK (status = ANY (ARRAY['active'::text, 'removed'::text])));
-- Create index "idx_ingest_manifest_file_fw" to table: "manifest_file"
CREATE INDEX "idx_ingest_manifest_file_fw" ON "ingest"."manifest_file" ("framework_code", "version_label");
-- Create index "idx_ingest_manifest_file_status" to table: "manifest_file"
CREATE INDEX "idx_ingest_manifest_file_status" ON "ingest"."manifest_file" ("status");
