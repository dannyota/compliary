-- name: UpsertSourceFile :one
INSERT INTO bronze.source_file (manifest_rel_path, sha256, framework_code, version_label, doc_role, file_format, source_url, license_kind, retrieved_on, provenance_note, serve_gate)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (manifest_rel_path, sha256) DO UPDATE SET
    framework_code  = EXCLUDED.framework_code,
    version_label   = EXCLUDED.version_label,
    doc_role        = EXCLUDED.doc_role,
    file_format     = EXCLUDED.file_format,
    source_url      = EXCLUDED.source_url,
    license_kind    = EXCLUDED.license_kind,
    retrieved_on    = EXCLUDED.retrieved_on,
    provenance_note = EXCLUDED.provenance_note,
    serve_gate      = EXCLUDED.serve_gate,
    updated_at      = now()
RETURNING *;

-- name: GetSourceFile :one
SELECT * FROM bronze.source_file WHERE manifest_rel_path = $1 AND sha256 = $2;

-- name: ListSourceFiles :many
SELECT * FROM bronze.source_file ORDER BY manifest_rel_path, created_at;

-- name: UpsertRawExtract :one
INSERT INTO bronze.raw_extract (source_file_id, kind, content, content_jsonb)
VALUES ($1, $2, $3, $4)
ON CONFLICT (source_file_id, kind) DO UPDATE SET
    content       = EXCLUDED.content,
    content_jsonb = EXCLUDED.content_jsonb,
    updated_at    = now()
RETURNING id;

-- name: GetRawExtract :one
SELECT * FROM bronze.raw_extract WHERE source_file_id = $1 AND kind = $2;
