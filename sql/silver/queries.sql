-- name: UpsertDocument :one
INSERT INTO silver.document (doc_key, framework_code, version_label, doc_role, qualifier, title, source_file_sha256, serve_gate, markdown)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (doc_key) DO UPDATE SET
    title              = EXCLUDED.title,
    source_file_sha256 = EXCLUDED.source_file_sha256,
    serve_gate         = EXCLUDED.serve_gate,
    markdown           = EXCLUDED.markdown,
    updated_at         = now()
RETURNING *;

-- name: GetDocumentByKey :one
SELECT * FROM silver.document WHERE doc_key = $1;

-- name: ListDocuments :many
SELECT * FROM silver.document ORDER BY framework_code, version_label, doc_role, qualifier;

-- name: ListDocumentsForVersion :many
SELECT * FROM silver.document WHERE framework_code = $1 AND version_label = $2
ORDER BY doc_role, qualifier;

-- Re-normalize is delete-and-rebuild per document: deterministic parsers make
-- the control tree reproducible, and ON DELETE CASCADE clears mappings/topics.

-- name: DeleteControlsForDocument :execrows
DELETE FROM silver.control WHERE document_id = $1;

-- name: InsertControl :one
INSERT INTO silver.control (document_id, parent_control_id, citation, citation_norm, kind, status, title, title_original, body, amends_citation_norm, amend_action, ordinal)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id;

-- name: ListControlsForDocument :many
SELECT * FROM silver.control WHERE document_id = $1 ORDER BY ordinal, id;

-- name: GetControlByCitationNorm :one
SELECT * FROM silver.control WHERE document_id = $1 AND citation_norm = $2;

-- name: CountControlsForDocument :one
SELECT count(*) FROM silver.control WHERE document_id = $1;

-- name: UpsertVersionRelation :exec
INSERT INTO silver.version_relation (from_framework_code, from_version_label, to_framework_code, to_version_label, relation_type, note)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (from_framework_code, from_version_label, to_framework_code, to_version_label, relation_type)
DO UPDATE SET note = EXCLUDED.note;

-- name: ListVersionRelations :many
SELECT * FROM silver.version_relation ORDER BY from_framework_code, from_version_label;

-- name: UpsertControlMapping :exec
INSERT INTO silver.control_mapping (from_control_id, to_framework_code, to_version_label, to_citation_norm, mapping_source_code, relationship, provenance_detail)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (from_control_id, to_framework_code, to_version_label, to_citation_norm, mapping_source_code)
DO UPDATE SET
    relationship      = EXCLUDED.relationship,
    provenance_detail = EXCLUDED.provenance_detail,
    updated_at        = now();

-- name: ResolveControlMappings :execrows
-- Lazily fill to_control_id for edges whose target framework has landed. The
-- join pins the target document's version when the edge is version-pinned;
-- version-unspecified edges resolve against the target framework's current
-- version (config.framework_version.is_current) so they never match a
-- superseded version's controls.
UPDATE silver.control_mapping cm
SET to_control_id = c.id
FROM silver.control c
JOIN silver.document d ON d.id = c.document_id
WHERE cm.to_control_id IS NULL
  AND d.framework_code = cm.to_framework_code
  AND c.citation_norm = cm.to_citation_norm
  AND (
      (cm.to_version_label IS NOT NULL AND d.version_label = cm.to_version_label)
   OR (cm.to_version_label IS NULL AND d.version_label = (
          SELECT fv.version_label FROM config.framework_version fv
          WHERE fv.framework_code = cm.to_framework_code AND fv.is_current
      ))
  );

-- name: ListMappingsForControl :many
SELECT * FROM silver.control_mapping WHERE from_control_id = $1
ORDER BY to_framework_code, to_citation_norm;

-- name: CountUnresolvedMappings :one
SELECT count(*) FROM silver.control_mapping WHERE to_control_id IS NULL;

-- name: ListEquivalentBodies :many
-- For each given control, the body of its longest resolved `equivalent`-mapped
-- counterpart — used by the Index stage to enrich shallow chunks (a 27001
-- Annex A one-liner gains its 27002 guidance). Only counterparts whose body is
-- longer than the control's own qualify, so the enrichment never flows from the
-- richer control to the poorer one. DISTINCT ON keeps one row per source.
SELECT DISTINCT ON (cm.from_control_id)
    cm.from_control_id,
    tgt.citation       AS equivalent_citation,
    d.framework_code   AS equivalent_framework,
    tgt.body           AS equivalent_body
FROM silver.control_mapping cm
JOIN silver.control tgt ON tgt.id = cm.to_control_id
JOIN silver.document d  ON d.id = tgt.document_id
JOIN silver.control src ON src.id = cm.from_control_id
WHERE cm.from_control_id = ANY($1::bigint[])
  AND cm.relationship = 'equivalent'
  AND tgt.body IS NOT NULL AND tgt.body <> ''
  AND length(tgt.body) > COALESCE(length(src.body), 0)
ORDER BY cm.from_control_id, length(tgt.body) DESC;
