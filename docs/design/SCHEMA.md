# Data model (schema design)

compliary's PostgreSQL schema — banhmi's conventions ported (crawl machinery dropped): sqlc, no
cross-schema FKs (cross-layer links are business-key `BIGINT`s), JSONB for non-queryable data,
single-column surrogate PKs, natural keys as composite `UNIQUE`, named constraints ≤63 bytes.
Reviewed 2026-07-19 (two-agent design review); fixes from that review are folded in below.

Schemas: **`ingest`** (file manifest + pipeline state) → **`bronze`** (raw files + license
provenance) → **`silver`** (frameworks/versions/documents/controls + relations + mappings) →
**`gold`** (chunks + vectors), plus **`config`** (the framework registry + vocabularies).

## `config` — the framework registry (seeded, operator-overridable)

The registry is data, not Go. CSVs in `deploy/seed/`; `origin='seed'|'user'` + `enabled` exactly as
banhmi (re-seed replaces seed rows, never touches user rows).

| Table | Role | Key columns | Unique |
|-------|------|-------------|--------|
| `framework` | One row per framework | `code` (`iso27001`, `soc2tsc`, `pcidss`, `nistcsf`, `nist80053`, `ciscontrols`, `iso27002`, `iso27017`, `iso27018`, `iso27701`, `iso22301`, `iso42001`, `swiftcscf`, `csaccm`, `cobit`) · `name` · `publisher` · `source_access` (`auto-fetch`/`form-gated`/`byo`) · `license_class` (`public-domain`/`open-restricted`/`licensed`) · **`ingest_enabled`** (default true — operator opt-out; the pipeline warns on frameworks whose terms restrict knowledge-base use, e.g. AICPA TSC, and the operator owns the choice) · **`serve_policy`** (`full` / `auth-text-only` / `operator-assumes-risk`) · `citation_scheme` (parser + cite-format key) | `(code)` |
| `framework_version` | One row per published version | `framework_code` · `version_label` (`2022`, `v8.1.2`, `2.0`, `v4.0.1`) · `published_on` · `is_current` · `edition_note` | `(framework_code, version_label)` |
| `mapping_source` | Provenance vocab for mapping edges | `code` (`nist-olir`, `csa-ccm-v4.1`, `cis-v8.1-mappings`, `publisher-catalog`, `operator`) · `name` · `authority_note` | `(code)` |
| `control_kind` | Vocab for `silver.control.kind` | `code` (`domain`/`family`/`clause`/`control`/`enhancement`/`criterion`/`point-of-focus`/`requirement`/`objective`/`practice`/`safeguard`/`annex-control`) — COBIT practices (`EDM01.01`) and TSC points of focus are first-class citable units, not body text | `(code)` |
| `setting` | key/value gates | `key`, `value` | `(key)` |

Supersession is derived from `framework_version` ordering **plus** explicit
`silver.version_relation` rows (a version can be superseded by a doc outside simple ordering,
e.g. amendments).

## `ingest` — file manifest (the whole pipeline state)

No discovery, no leases. One table.

| Table | Role | Key columns |
|-------|------|-------------|
| `manifest_file` | One row per file under `data/` | `rel_path` UNIQUE · `sha256` · `size_bytes` · `framework_code` + `version_label` + `doc_role` (matched from registry patterns; NULL = unrecognized → a quality gap) · `file_format` (`oscal-json`/`xlsx`/`pdf`) · **`status`** (`active`/`removed` — a vanished/renamed path is demoted, never deleted; history survives renames) · per-stage state: `extracted_at`/`normalized_at`/`indexed_at` + `stage_error` · re-run diff: state resets when `sha256` changes |

Scan-time ambiguity rule: two `active` files resolving to the same
`(framework_code, version_label, doc_role, file_format)` is an error — the scan reports it as a
quality gap and processes neither until the operator resolves it (prevents duplicate control trees).

Completeness = every `manifest_file` row for an `is_current` version reaching `indexed_at`,
recomputed by query — never a stored boolean. Pipeline stages iterate all eligible rows, record
per-row errors, and continue; `cmd/pipeline` exits non-zero with a N-succeeded/M-failed summary.
`stage_error` and quality-gap messages carry citations, paths, and line numbers — **never verbatim
document text** (log-leak rule).

## `bronze` — raw capture + license provenance

| Table | Role | Notes |
|-------|------|-------|
| `source_file` | One row per ingested file observation | `manifest_rel_path` · `sha256` · `framework_code`/`version_label` · **license provenance:** `source_url` (official publisher page), `license_kind` (verbatim class: `public-domain`/`cc-by-nc-nd`/`click-through`/`purchased`/`membership`/`unverified`), `retrieved_on`, `provenance_note` (e.g. "ITU X.1631 co-publication", "operator-accepted re-hosted copy") · **`serve_gate`** (`public`/`auth-only`) — the read path enforces this per document |
| `raw_extract` | Extracted raw structures per file | `kind` (`text-markdown`/`oscal-catalog-json`/`workbook-rows-json`) · `content` / `content_jsonb` · UNIQUE `(source_file_id, kind)` (idempotent re-extract) |

## `silver` — frameworks, controls, versions, mappings

| Table | Role | Notes |
|-------|------|-------|
| `document` | One publication (framework version's document) | `doc_key` = `<framework_code>|<version_label>|<doc_role>[:<qualifier>]` — **amendments attach to the base version they modify** (`iso27001|2022|amendment:amd1-2024`), so the base linkage is explicit · `doc_role` (`main`/`amendment`/`companion-workbook`/`changelog`) · title · `source_file_sha256` business key · denormalized display `markdown` |
| `control` | **The citation unit** | `document_id` · `citation` UNIQUE per `(document_id, citation)` (`A.5.1`, `AC-2(3)`, `CC6.1`, `8.3.6`, `PR.AA-01`, `CLD.6.3.1`, `AIS-01`, `EDM01.01`, `5.1`) · `citation_norm` (uppercased, separator-normalized — the mapping join key; **scoped per framework, never globally unique** — 27017's 2013-numbered `6.1.1` ≠ 27001:2022's `6.1`) · `parent_control_id` (tree: domain → family → control → enhancement; objective → practice; criterion → point-of-focus) · `kind` (from `config.control_kind`) · **`status`** (`active`/`withdrawn`/`deprecated` — 800-53r5 has 182 withdrawn controls; incorporation edges go to `control_mapping` as `incorporated-into`) · **`title`** (always ours/paraphrased for `licensed` frameworks — public-safe by construction; may equal publisher's only for `public-domain`) · **`title_original`** (publisher's verbatim heading, nullable — auth-gated like `body`) · `body` (verbatim normative text — **auth-gated at serve time by the document's `serve_gate`**) · **`amends_citation_norm` + `amend_action`** (`add`/`replace`/`delete`, nullable — set only on controls inside amendment documents, linking each patch to the base control it modifies; a query for "current 27001 4.1" joins base + amendment deterministically) · `ordinal` |
| `version_relation` | Version lineage edges | `from_framework_code`+`from_version` → `to_framework_code`+`to_version` · `relation_type` (`supersedes`/`amends`/`consolidates`) · `note` (e.g. "cancels and replaces; realigned to 27002:2022") — populated from registry knowledge + publisher forewords · endpoints validated against `config.framework_version` at pipeline time; unresolved → quality gap |
| `control_mapping` | **Cross-framework edges, the product's second half** | `from_control_id` → `to_framework_code` + **`to_version_label`** (nullable = version-unspecified; the real data is version-specific — CSF 2.0's workbook maps separately to 800-53 r5.1.1 and r5.2.0) + `to_citation_norm` (business key — target may not be ingested yet, banhmi's `doc_ref` stub pattern flattened) · `mapping_source_code` (config vocab) · `relationship` (`equivalent`/`subset-of`/`superset-of`/`intersects`/`related`/`incorporated-into`) · `provenance_detail` (row/cell reference only — never quoted text) · resolved `to_control_id` nullable, filled when the target framework lands · candidate key `(from_control_id, to_framework_code, to_version_label, to_citation_norm, mapping_source_code)` |
| `control_topic` | Optional theme tags | deferred vocabulary; schema-ready |

Key differences from banhmi's silver: no gazette/alias/text-authority machinery (single
authoritative file per document, no reconcile); validity collapses into `version_relation` +
`framework_version.is_current` (frameworks don't get partially repealed — a version is current or
superseded; amendments are `amends` edges with their own document).

## `gold` — chunks + vectors

| Table | Role | Notes |
|-------|------|-------|
| `chunk` | Control-level chunks | `control_id` · `citation` (display cite: `ISO/IEC 27001:2022 A.5.1`, `NIST SP 800-53r5 AC-2(3)`) · `context_prefix` (framework + version + family/clause path + **paraphrased** `title` only — never `title_original`) · `content` · `content_sparse` (`sparsevec` BM25) · UNIQUE `(control_id, ordinal)` (one chunk per control today; ordinal reserved for deep controls like COBIT objectives, no migration later) |
| `chunk_embedding` | Dense vectors | Qwen3-Embedding 1024-d · one row per `(chunk_id, model)` · HNSW cosine |

Serve-time rule: unauthenticated callers get citations, **paraphrased titles**, scores, and mapping
edges for everything; chunk `content`, `body`, `title_original`, and `context_prefix` for
`auth-only` documents are returned only past auth — a public caller gets the citation + a gap
notice, never licensed text. (The maintainer instance is auth-only anyway; this rule is the safe
default for every self-deployed operator.)

## Parsing order (M2 build order)

1. **NIST SP 800-53 r5 — OSCAL JSON** (`nist-sp-800-53r5-oscal-catalog.json`): structured catalog →
   `control` tree with enhancements. Richest, zero PDF risk. Proves the whole schema.
2. **NIST CSF 2.0 — XLSX** (`nist-csf-2.0.xlsx`): Function.Category.Subcategory rows.
3. **CIS Controls v8.1.2 — XLSX**: Control/Safeguard rows (asset class, IG columns).
4. **CSA CCM v4.1 — XLSX**: control rows **+ the Scope Applicability mapping sheet → `control_mapping`**.
5. **PCI DSS v4.0.1 — PDF**: requirement tree (`8.3.6`) via go-fitz + layout rules.
6. **AICPA TSC — PDF**: criteria (`CC6.1`) + points of focus.
7. **ISO family — PDF**: clause/annex-control trees (27001/27002/27017/27018; 22301 body waits on
   phase-2 purchase).
8. **COBIT 2019 — PDF**: objectives (`EDM01`–`MEA04`) + practices.

Each parser lands with golden-count tests (expected control counts per version — e.g. 27001:2022
Annex A = 93, CCM v4.1 = 192, CSF 2.0 subcategories = 106) — parsed counts must match exactly.

## Key decisions

1. **Manifest, not crawler** — `ingest` is one table; sha256 diff drives re-runs; renamed/removed
   paths are demoted (`status='removed'`), never deleted.
2. **`control` is the atom** — every framework's citation unit normalizes into one tree-shaped
   table discriminated by config-seeded `kind` (incl. COBIT practices, TSC points of focus);
   the citation string is the natural key, scoped per framework.
3. **Mappings are business-keyed and version-aware** (`to_framework_code, to_version_label,
   to_citation_norm`), resolve lazily — mapping workbooks load before/without the target
   framework's text (mirrors banhmi's `doc_ref` stubs).
4. **License gate lives on bronze, enforced at serve** — `serve_gate` flows bronze → silver
   document → MCP; `title` is paraphrased-by-construction for licensed frameworks, so every
   public-safe field is public-safe by schema, not by discipline.
5. **Version currency is registry + edges** — no bitemporal machinery; `is_current` +
   `version_relation` covers the framework world's actual dynamics. Amendment patches link to
   base controls via `amends_citation_norm` + `amend_action`. Superseded versions are demoted
   and served flagged — never deleted, never presented as current.
6. **Golden counts gate every parser** — exact expected control counts per framework version.
   Retrieval golden sets (M3) are **citation-keyed** — `{query → framework|version|citation}`,
   optional content sha256 for integrity — never verbatim licensed text.
7. **Operator owns restricted-terms choices** — `ingest_enabled`/`serve_policy` per framework;
   the pipeline warns on AICPA TSC's knowledge-base clause instead of deciding silently.
