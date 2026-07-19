CREATE SCHEMA IF NOT EXISTS config;

-- config is the framework registry + vocabularies — the "no hardcoded policy
-- lists" home. Rows with origin='seed' are replaced by `cmd/seed` (which reads
-- deploy/seed/*.csv); origin='user' rows are never touched, so operator
-- customizations survive a re-seed.

-- config.framework: one row per framework (the registry dimension). code is the
-- stable business key used across all layers (silver.document.framework_code,
-- mappings, manifest matching). ingest_enabled is the operator opt-out for
-- frameworks whose terms restrict knowledge-base use; a non-empty terms_note
-- makes the pipeline print that warning — data-driven, never a Go list.
-- serve_policy: 'full' = text servable without auth (public-domain / license
-- permits); 'auth-text-only' = citations+paraphrased titles public, text behind
-- auth; 'operator-assumes-risk' = operator explicitly overrode a restriction.
CREATE TABLE config.framework (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code            TEXT NOT NULL,
    name            TEXT NOT NULL,
    publisher       TEXT NOT NULL,
    source_access   TEXT NOT NULL,
    license_class   TEXT NOT NULL,
    ingest_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    serve_policy    TEXT NOT NULL,
    citation_scheme TEXT NOT NULL,
    terms_note      TEXT NOT NULL DEFAULT '',
    origin          TEXT NOT NULL DEFAULT 'seed',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_framework UNIQUE (code),
    CONSTRAINT chk_config_framework_access CHECK (source_access IN ('auto-fetch', 'form-gated', 'byo')),
    CONSTRAINT chk_config_framework_license CHECK (license_class IN ('public-domain', 'open-restricted', 'licensed')),
    CONSTRAINT chk_config_framework_serve CHECK (serve_policy IN ('full', 'auth-text-only', 'operator-assumes-risk')),
    CONSTRAINT chk_config_framework_origin CHECK (origin IN ('seed', 'user'))
);

-- config.framework_version: one row per published version. published_on is set
-- only when the publisher states an exact day; month-precision publication info
-- lives in edition_note (never a fabricated day). Supersession is derived from
-- these rows plus explicit silver.version_relation edges. The partial unique
-- index guarantees at most one current version per framework.
CREATE TABLE config.framework_version (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    framework_code TEXT NOT NULL,
    version_label  TEXT NOT NULL,
    published_on   DATE,
    is_current     BOOLEAN NOT NULL DEFAULT FALSE,
    edition_note   TEXT NOT NULL DEFAULT '',
    origin         TEXT NOT NULL DEFAULT 'seed',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_framework_version UNIQUE (framework_code, version_label),
    CONSTRAINT chk_config_framework_version_origin CHECK (origin IN ('seed', 'user'))
);

CREATE UNIQUE INDEX uq_config_framework_version_current
    ON config.framework_version (framework_code) WHERE is_current;

-- config.mapping_source: provenance vocabulary for silver.control_mapping edges.
CREATE TABLE config.mapping_source (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code           TEXT NOT NULL,
    name           TEXT NOT NULL,
    authority_note TEXT NOT NULL DEFAULT '',
    origin         TEXT NOT NULL DEFAULT 'seed',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_mapping_source UNIQUE (code),
    CONSTRAINT chk_config_mapping_source_origin CHECK (origin IN ('seed', 'user'))
);

-- config.control_kind: vocabulary for silver.control.kind. Sub-control units
-- (COBIT practices, TSC points of focus) are first-class citable kinds, not
-- body text. Validated at pipeline time (no cross-schema FK).
CREATE TABLE config.control_kind (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code       TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    origin     TEXT NOT NULL DEFAULT 'seed',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_control_kind UNIQUE (code),
    CONSTRAINT chk_config_control_kind_origin CHECK (origin IN ('seed', 'user'))
);

-- config.file_rule: filename-to-document matching rules for the manifest
-- scanner. Each rule maps a data/ rel_path pattern (path.Match glob) to a
-- framework + version + role + format, or marks it as ignored. First match
-- by ordinal wins; unmatched files are recorded with NULL framework fields
-- (quality gap). Seed rules cover the shipped corpus; operators add rows
-- with origin='user' to extend without editing seeds.
CREATE TABLE config.file_rule (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ordinal         INT NOT NULL,
    pattern         TEXT NOT NULL,
    framework_code  TEXT,
    version_label   TEXT,
    doc_role        TEXT,
    qualifier       TEXT NOT NULL DEFAULT '',
    file_format     TEXT,
    ignore          BOOLEAN NOT NULL DEFAULT FALSE,
    ignore_reason   TEXT NOT NULL DEFAULT '',
    license_kind    TEXT,
    source_url      TEXT NOT NULL DEFAULT '',
    provenance_note TEXT NOT NULL DEFAULT '',
    origin          TEXT NOT NULL DEFAULT 'seed',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_file_rule UNIQUE (pattern),
    CONSTRAINT chk_config_file_rule_origin CHECK (origin IN ('seed', 'user')),
    CONSTRAINT chk_config_file_rule_role CHECK (doc_role IN ('main', 'amendment', 'companion-workbook', 'changelog', 'guide')),
    CONSTRAINT chk_config_file_rule_format CHECK (file_format IN ('oscal-json', 'xlsx', 'pdf')),
    CONSTRAINT chk_config_file_rule_license CHECK (license_kind IN ('public-domain', 'cc-by-nc-nd', 'click-through', 'purchased', 'unverified')),
    CONSTRAINT chk_config_file_rule_ignore CHECK (
        (ignore = TRUE AND framework_code IS NULL AND version_label IS NULL AND doc_role IS NULL AND file_format IS NULL)
        OR
        (ignore = FALSE AND framework_code IS NOT NULL AND version_label IS NOT NULL AND doc_role IS NOT NULL AND file_format IS NOT NULL)
    )
);

-- config.reference_source: maps informative-reference prefixes (colon-split
-- first field) to registry targets. Each row enables the CSF normalizer to
-- emit cross-framework mapping edges for lines matching that prefix. prefix
-- is the exact colon-split match key; to_version_label NULL means the source
-- does not pin a version (resolved lazily via is_current). Prefixes with no
-- enabled row are skipped and counted — never guessed.
CREATE TABLE config.reference_source (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    prefix              TEXT NOT NULL,
    to_framework_code   TEXT NOT NULL,
    to_version_label    TEXT,
    mapping_source_code TEXT NOT NULL DEFAULT 'publisher-catalog',
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    origin              TEXT NOT NULL DEFAULT 'seed',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_reference_source UNIQUE (prefix),
    CONSTRAINT chk_config_reference_source_origin CHECK (origin IN ('seed', 'user'))
);

-- config.control_title: curated paraphrased titles for controls in licensed
-- frameworks. Seeds carry our own topical summaries — never verbatim publisher
-- headings. The normalizer looks up (framework_code, version_label,
-- citation_norm) at write time and uses the curated title as silver.control.title
-- when present, falling back to the parser's neutral label otherwise.
-- origin='seed' rows are replaced by re-seed; origin='user' survive.
CREATE TABLE config.control_title (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    framework_code  TEXT NOT NULL,
    version_label   TEXT NOT NULL,
    citation_norm   TEXT NOT NULL,
    title           TEXT NOT NULL,
    origin          TEXT NOT NULL DEFAULT 'seed',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_control_title UNIQUE (framework_code, version_label, citation_norm),
    CONSTRAINT chk_config_control_title_origin CHECK (origin IN ('seed', 'user'))
);

-- config.setting: generic key/value store for operator-tunable gates.
CREATE TABLE config.setting (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    origin     TEXT NOT NULL DEFAULT 'seed',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_config_setting UNIQUE (key),
    CONSTRAINT chk_config_setting_origin CHECK (origin IN ('seed', 'user'))
);
