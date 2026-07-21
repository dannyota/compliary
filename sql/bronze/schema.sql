CREATE SCHEMA IF NOT EXISTS bronze;

-- bronze is raw capture + license provenance. The license gate lives here and
-- flows bronze → silver.document → MCP serve time. No pipeline state in bronze
-- (that is ingest.manifest_file's job).

-- bronze.source_file: one row per ingested file observation, keyed by
-- (manifest_rel_path, sha256) so a re-drop of a changed file is a new
-- observation and history is preserved. License provenance is first-class:
-- source_url is the official publisher page, license_kind the verbatim class,
-- provenance_note the human trail (e.g. "ITU-T X.1631 co-publication",
-- "operator-accepted re-hosted copy"). serve_gate defaults to 'auth-only' —
-- public serving is opt-in per file, never the default.
CREATE TABLE bronze.source_file (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    manifest_rel_path TEXT NOT NULL,
    sha256            TEXT NOT NULL,
    framework_code    TEXT NOT NULL,
    version_label     TEXT NOT NULL,
    doc_role          TEXT NOT NULL DEFAULT 'main',
    file_format       TEXT NOT NULL,
    source_url        TEXT NOT NULL DEFAULT '',
    license_kind      TEXT NOT NULL DEFAULT 'unverified',
    retrieved_on      DATE,
    provenance_note   TEXT NOT NULL DEFAULT '',
    serve_gate        TEXT NOT NULL DEFAULT 'auth-only',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_bronze_source_file UNIQUE (manifest_rel_path, sha256),
    CONSTRAINT chk_bronze_source_file_role CHECK (doc_role IN ('main', 'amendment', 'companion-workbook', 'changelog')),
    CONSTRAINT chk_bronze_source_file_format CHECK (file_format IN ('oscal-json', 'xlsx', 'pdf')),
    CONSTRAINT chk_bronze_source_file_license CHECK (license_kind IN ('public-domain', 'cc-by-nc-nd', 'click-through', 'purchased', 'membership', 'unverified')),
    CONSTRAINT chk_bronze_source_file_gate CHECK (serve_gate IN ('public', 'auth-only'))
);

CREATE INDEX idx_bronze_source_file_fw ON bronze.source_file (framework_code, version_label);

-- bronze.raw_extract: extracted raw structures per source file. Exactly one row
-- per (source_file, kind) — re-extract is an idempotent upsert. content holds
-- text kinds (markdown); content_jsonb holds structured kinds (OSCAL catalog,
-- workbook rows).
CREATE TABLE bronze.raw_extract (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_file_id BIGINT NOT NULL,
    kind           TEXT NOT NULL,
    content        TEXT,
    content_jsonb  JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_bronze_raw_extract_file FOREIGN KEY (source_file_id)
        REFERENCES bronze.source_file (id) ON DELETE CASCADE,
    CONSTRAINT uq_bronze_raw_extract UNIQUE (source_file_id, kind),
    CONSTRAINT chk_bronze_raw_extract_kind CHECK (kind IN ('text-markdown', 'oscal-catalog-json', 'workbook-rows-json', 'pdf-pages-json'))
);

CREATE INDEX idx_bronze_source_file_sha256 ON bronze.source_file (sha256);
