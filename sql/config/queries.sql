-- Load queries (read the registry into the app at startup).

-- name: ListFrameworks :many
SELECT * FROM config.framework ORDER BY code;

-- name: GetFramework :one
SELECT * FROM config.framework WHERE code = $1;

-- name: ListFrameworkVersions :many
SELECT * FROM config.framework_version ORDER BY framework_code, version_label;

-- name: ListCurrentFrameworkVersions :many
SELECT * FROM config.framework_version WHERE is_current ORDER BY framework_code;

-- name: GetFrameworkVersion :one
SELECT * FROM config.framework_version WHERE framework_code = $1 AND version_label = $2;

-- name: ListMappingSources :many
SELECT * FROM config.mapping_source ORDER BY code;

-- name: ListControlKinds :many
SELECT code FROM config.control_kind ORDER BY code;

-- name: GetSetting :one
SELECT value FROM config.setting WHERE key = $1;

-- name: ListAllFileRules :many
SELECT * FROM config.file_rule ORDER BY ordinal;

-- Seed queries (cmd/seed). Each re-seed deletes the managed ('seed') rows and
-- re-inserts from the CSV; ON CONFLICT DO NOTHING means a user override sharing
-- a natural key is preserved. origin='user' rows are never deleted.

-- name: DeleteSeedFrameworks :exec
DELETE FROM config.framework WHERE origin = 'seed';

-- name: InsertSeedFramework :exec
INSERT INTO config.framework (code, name, publisher, source_access, license_class, ingest_enabled, serve_policy, citation_scheme, terms_note, origin)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'seed') ON CONFLICT (code) DO NOTHING;

-- name: DeleteSeedFrameworkVersions :exec
DELETE FROM config.framework_version WHERE origin = 'seed';

-- name: InsertSeedFrameworkVersion :exec
INSERT INTO config.framework_version (framework_code, version_label, published_on, is_current, edition_note, origin)
VALUES ($1, $2, $3, $4, $5, 'seed') ON CONFLICT (framework_code, version_label) DO NOTHING;

-- name: DeleteSeedMappingSources :exec
DELETE FROM config.mapping_source WHERE origin = 'seed';

-- name: InsertSeedMappingSource :exec
INSERT INTO config.mapping_source (code, name, authority_note, origin)
VALUES ($1, $2, $3, 'seed') ON CONFLICT (code) DO NOTHING;

-- name: DeleteSeedControlKinds :exec
DELETE FROM config.control_kind WHERE origin = 'seed';

-- name: InsertSeedControlKind :exec
INSERT INTO config.control_kind (code, note, origin)
VALUES ($1, $2, 'seed') ON CONFLICT (code) DO NOTHING;

-- name: DeleteSeedSettings :exec
DELETE FROM config.setting WHERE origin = 'seed';

-- name: InsertSeedSetting :exec
INSERT INTO config.setting (key, value, origin)
VALUES ($1, $2, 'seed') ON CONFLICT (key) DO NOTHING;

-- name: ListReferenceSources :many
SELECT * FROM config.reference_source WHERE enabled ORDER BY prefix;

-- name: DeleteSeedReferenceSources :exec
DELETE FROM config.reference_source WHERE origin = 'seed';

-- name: InsertSeedReferenceSource :exec
INSERT INTO config.reference_source (prefix, to_framework_code, to_version_label, mapping_source_code, enabled, origin)
VALUES ($1, $2, $3, $4, $5, 'seed') ON CONFLICT (prefix) DO NOTHING;

-- name: DeleteSeedFileRules :exec
DELETE FROM config.file_rule WHERE origin = 'seed';

-- name: InsertSeedFileRule :exec
INSERT INTO config.file_rule (ordinal, pattern, framework_code, version_label, doc_role, qualifier, file_format, ignore, ignore_reason, license_kind, source_url, provenance_note, origin)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'seed') ON CONFLICT (pattern) DO NOTHING;
