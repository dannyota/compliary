-- Manifest scan. The upsert preserves per-stage state when the sha256 is
-- unchanged and resets it when the content changed, so a rescan re-runs only
-- what actually moved.

-- name: UpsertManifestFile :one
INSERT INTO ingest.manifest_file (rel_path, sha256, size_bytes, framework_code, version_label, doc_role, qualifier, file_format, status, ignored, ignore_reason)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', $9, $10)
ON CONFLICT (rel_path) DO UPDATE SET
    sha256         = EXCLUDED.sha256,
    size_bytes     = EXCLUDED.size_bytes,
    framework_code = EXCLUDED.framework_code,
    version_label  = EXCLUDED.version_label,
    doc_role       = EXCLUDED.doc_role,
    qualifier      = EXCLUDED.qualifier,
    file_format    = EXCLUDED.file_format,
    status         = 'active',
    ignored        = EXCLUDED.ignored,
    ignore_reason  = EXCLUDED.ignore_reason,
    extracted_at   = CASE WHEN ingest.manifest_file.sha256 = EXCLUDED.sha256 THEN ingest.manifest_file.extracted_at END,
    normalized_at  = CASE WHEN ingest.manifest_file.sha256 = EXCLUDED.sha256 THEN ingest.manifest_file.normalized_at END,
    indexed_at     = CASE WHEN ingest.manifest_file.sha256 = EXCLUDED.sha256 THEN ingest.manifest_file.indexed_at END,
    stage_error    = CASE WHEN ingest.manifest_file.sha256 = EXCLUDED.sha256 THEN ingest.manifest_file.stage_error ELSE '' END,
    updated_at     = now()
RETURNING *;

-- name: DemoteMissingManifestFiles :execrows
-- Demote active rows whose path was not seen in this scan. Never deletes.
UPDATE ingest.manifest_file SET status = 'removed', updated_at = now()
WHERE status = 'active' AND rel_path != ALL($1::text[]);

-- name: ListActiveManifestFiles :many
SELECT * FROM ingest.manifest_file WHERE status = 'active' ORDER BY rel_path;

-- name: ListUnrecognizedManifestFiles :many
SELECT * FROM ingest.manifest_file
WHERE status = 'active' AND NOT ignored AND framework_code IS NULL ORDER BY rel_path;

-- name: ListIgnoredManifestFiles :many
SELECT * FROM ingest.manifest_file
WHERE status = 'active' AND ignored ORDER BY rel_path;

-- name: ListFilesToExtract :many
SELECT * FROM ingest.manifest_file
WHERE status = 'active' AND framework_code IS NOT NULL AND NOT ignored
  AND doc_role NOT IN ('guide', 'changelog')
  AND extracted_at IS NULL
ORDER BY rel_path;

-- name: ListFilesToNormalize :many
SELECT * FROM ingest.manifest_file
WHERE status = 'active' AND framework_code IS NOT NULL AND NOT ignored
  AND doc_role NOT IN ('guide', 'changelog')
  AND extracted_at IS NOT NULL AND normalized_at IS NULL
ORDER BY rel_path;

-- name: ListFilesToIndex :many
SELECT * FROM ingest.manifest_file
WHERE status = 'active' AND framework_code IS NOT NULL AND NOT ignored
  AND doc_role NOT IN ('guide', 'changelog')
  AND normalized_at IS NOT NULL AND indexed_at IS NULL
ORDER BY rel_path;

-- name: MarkExtracted :exec
UPDATE ingest.manifest_file SET extracted_at = now(), stage_error = '', updated_at = now() WHERE id = $1;

-- name: MarkNormalized :exec
-- A rebuilt control tree invalidates existing chunks, so NULL indexed_at to
-- force re-indexing on the next pipeline run.
UPDATE ingest.manifest_file SET normalized_at = now(), indexed_at = NULL, stage_error = '', updated_at = now() WHERE id = $1;

-- name: MarkIndexed :exec
UPDATE ingest.manifest_file SET indexed_at = now(), stage_error = '', updated_at = now() WHERE id = $1;

-- name: SetStageError :exec
UPDATE ingest.manifest_file SET stage_error = $2, updated_at = now() WHERE id = $1;
