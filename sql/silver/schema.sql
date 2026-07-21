CREATE SCHEMA IF NOT EXISTS silver;

-- silver is the normalized knowledge base: framework → version → document →
-- control tree, version lineage, and cross-framework mappings. Cross-layer
-- links are business keys (framework_code, sha256, citation_norm), never
-- cross-schema FKs. Framework/version validity collapses into
-- config.framework_version.is_current + silver.version_relation — frameworks
-- are never partially repealed, so there is no bitemporal machinery.

-- silver.document: one publication (a framework version's document). doc_key =
-- <framework_code>|<version_label>|<doc_role>[:<qualifier>] — amendments attach
-- to the base version they modify (iso27001|2022|amendment:amd1-2024), so the
-- base linkage is explicit. serve_gate is denormalized from bronze.source_file
-- and enforced by the read path: title is public-safe by construction
-- (paraphrased for licensed frameworks); markdown/body are gated.
CREATE TABLE silver.document (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_key            TEXT NOT NULL,
    framework_code     TEXT NOT NULL,
    version_label      TEXT NOT NULL,
    doc_role           TEXT NOT NULL,
    qualifier          TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    source_file_sha256 TEXT NOT NULL,
    serve_gate         TEXT NOT NULL DEFAULT 'auth-only',
    markdown           TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_silver_document_key UNIQUE (doc_key),
    CONSTRAINT uq_silver_document UNIQUE (framework_code, version_label, doc_role, qualifier),
    CONSTRAINT chk_silver_document_role CHECK (doc_role IN ('main', 'amendment', 'companion-workbook', 'changelog')),
    CONSTRAINT chk_silver_document_gate CHECK (serve_gate IN ('public', 'auth-only'))
);

CREATE INDEX idx_silver_document_fw ON silver.document (framework_code, version_label);

-- silver.control: THE citation unit — every framework's citable atom in one
-- tree-shaped table discriminated by config-seeded kind (domain → family →
-- control → enhancement; objective → practice; criterion → point-of-focus).
-- citation is the publisher-native cite (A.5.1, AC-2(3), CC6.1, 8.3.6,
-- PR.AA-01, CLD.6.3.1, AIS-01, EDM01.01); citation_norm is the uppercased,
-- separator-normalized mapping join key — scoped per framework, never globally
-- unique (27017's 2013-numbered 6.1.1 ≠ 27001:2022's 6.1). title is always
-- ours/paraphrased for licensed frameworks (public-safe by construction);
-- title_original and body are verbatim publisher text, auth-gated by the
-- document's serve_gate. amends_citation_norm + amend_action are set only on
-- controls inside amendment documents, linking each patch to the base control
-- it modifies. status covers 800-53r5's 182 withdrawn controls; their
-- incorporated-into edges live in control_mapping.
CREATE TABLE silver.control (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id          BIGINT NOT NULL,
    parent_control_id    BIGINT,
    citation             TEXT NOT NULL,
    citation_norm        TEXT NOT NULL,
    kind                 TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'active',
    title                TEXT NOT NULL,
    title_original       TEXT,
    body                 TEXT,
    amends_citation_norm TEXT,
    amend_action         TEXT,
    ordinal              INTEGER NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_silver_control_document FOREIGN KEY (document_id)
        REFERENCES silver.document (id) ON DELETE CASCADE,
    CONSTRAINT fk_silver_control_parent FOREIGN KEY (parent_control_id)
        REFERENCES silver.control (id) ON DELETE CASCADE,
    CONSTRAINT uq_silver_control UNIQUE (document_id, citation),
    CONSTRAINT chk_silver_control_status CHECK (status IN ('active', 'withdrawn', 'deprecated')),
    CONSTRAINT chk_silver_control_amend CHECK (amend_action IN ('add', 'replace', 'delete')),
    CONSTRAINT chk_silver_control_amend_pair CHECK ((amends_citation_norm IS NULL) = (amend_action IS NULL))
);

CREATE INDEX idx_silver_control_document ON silver.control (document_id);
CREATE INDEX idx_silver_control_norm ON silver.control (citation_norm);
CREATE INDEX idx_silver_control_parent ON silver.control (parent_control_id);

-- silver.version_relation: version lineage edges (supersedes / amends /
-- consolidates), populated from registry knowledge + publisher forewords.
-- Endpoints are validated against config.framework_version at pipeline time;
-- an unresolved endpoint is a quality gap, not a silent insert.
CREATE TABLE silver.version_relation (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_framework_code TEXT NOT NULL,
    from_version_label  TEXT NOT NULL,
    to_framework_code   TEXT NOT NULL,
    to_version_label    TEXT NOT NULL,
    relation_type       TEXT NOT NULL,
    note                TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_silver_version_relation UNIQUE (from_framework_code, from_version_label, to_framework_code, to_version_label, relation_type),
    CONSTRAINT chk_silver_version_relation_type CHECK (relation_type IN ('supersedes', 'amends', 'consolidates'))
);

-- silver.control_mapping: cross-framework edges — the product's second half.
-- Business-keyed and version-aware: (to_framework_code, to_version_label,
-- to_citation_norm), with to_version_label NULL meaning the mapping source did
-- not pin a version. Mapping workbooks load before/without the target
-- framework's text; to_control_id resolves lazily when the target lands.
-- provenance_detail is a row/cell reference only — never quoted text.
CREATE TABLE silver.control_mapping (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_control_id     BIGINT NOT NULL,
    to_framework_code   TEXT NOT NULL,
    to_version_label    TEXT,
    to_citation_norm    TEXT NOT NULL,
    to_control_id       BIGINT,
    mapping_source_code TEXT NOT NULL,
    relationship        TEXT NOT NULL DEFAULT 'related',
    provenance_detail   TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_silver_mapping_from FOREIGN KEY (from_control_id)
        REFERENCES silver.control (id) ON DELETE CASCADE,
    CONSTRAINT fk_silver_mapping_to FOREIGN KEY (to_control_id)
        REFERENCES silver.control (id) ON DELETE SET NULL,
    CONSTRAINT uq_silver_control_mapping UNIQUE NULLS NOT DISTINCT (from_control_id, to_framework_code, to_version_label, to_citation_norm, mapping_source_code),
    CONSTRAINT chk_silver_mapping_rel CHECK (relationship IN ('equivalent', 'subset-of', 'superset-of', 'intersects', 'related', 'incorporated-into', 'moved-to'))
);

CREATE INDEX idx_silver_mapping_target ON silver.control_mapping (to_framework_code, to_citation_norm);
CREATE INDEX idx_silver_mapping_unresolved ON silver.control_mapping (to_framework_code) WHERE to_control_id IS NULL;

-- silver.control_topic: optional theme tags (vocabulary deferred; schema-ready).
CREATE TABLE silver.control_topic (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    control_id BIGINT NOT NULL,
    topic      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT fk_silver_control_topic FOREIGN KEY (control_id)
        REFERENCES silver.control (id) ON DELETE CASCADE,
    CONSTRAINT uq_silver_control_topic UNIQUE (control_id, topic)
);

CREATE INDEX idx_silver_mapping_to_control ON silver.control_mapping (to_control_id) WHERE to_control_id IS NOT NULL;
