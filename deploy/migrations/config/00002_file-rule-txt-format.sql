-- +goose Up
-- Modify "file_rule" table
ALTER TABLE "config"."file_rule" DROP CONSTRAINT "chk_config_file_rule_format", ADD CONSTRAINT "chk_config_file_rule_format" CHECK (file_format = ANY (ARRAY['oscal-json'::text, 'xlsx'::text, 'pdf'::text, 'txt'::text]));
