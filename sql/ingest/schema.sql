CREATE SCHEMA IF NOT EXISTS ingest;

-- ingest is a file manifest over data/, not a crawler — no cursors, no leases,
-- no watermarks. One row per file; sha256 diff drives re-runs. Completeness =
-- every active row for a current version reaching indexed_at, recomputed by
-- query — never a stored boolean.

-- ingest.manifest_file: one row per file ever seen under data/. framework_code /
-- version_label / doc_role are matched from the registry naming convention
-- (<publisher>/<framework>-<version>[-<doctype>].<ext>); NULL = unrecognized,
-- reported as a quality gap, never processed. A vanished or renamed path is
-- demoted to status='removed', never deleted (history survives renames). When a
-- rescan sees a changed sha256, per-stage timestamps reset so the file re-runs
-- every stage. stage_error carries citations/paths/line numbers only — never
-- verbatim document text (log-leak rule).
CREATE TABLE ingest.manifest_file (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    rel_path       TEXT NOT NULL,
    sha256         TEXT NOT NULL,
    size_bytes     BIGINT NOT NULL DEFAULT 0,
    framework_code TEXT,
    version_label  TEXT,
    doc_role       TEXT,
    file_format    TEXT,
    status         TEXT NOT NULL DEFAULT 'active',
    extracted_at   TIMESTAMPTZ,
    normalized_at  TIMESTAMPTZ,
    indexed_at     TIMESTAMPTZ,
    stage_error    TEXT NOT NULL DEFAULT '',
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_ingest_manifest_file UNIQUE (rel_path),
    CONSTRAINT chk_ingest_manifest_file_status CHECK (status IN ('active', 'removed')),
    CONSTRAINT chk_ingest_manifest_file_role CHECK (doc_role IN ('main', 'amendment', 'companion-workbook', 'changelog')),
    CONSTRAINT chk_ingest_manifest_file_format CHECK (file_format IN ('oscal-json', 'xlsx', 'pdf'))
);

CREATE INDEX idx_ingest_manifest_file_fw ON ingest.manifest_file (framework_code, version_label);
CREATE INDEX idx_ingest_manifest_file_status ON ingest.manifest_file (status);
