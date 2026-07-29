# Data model (schema design)

compliary's PostgreSQL schema — banhmi's conventions ported (crawl machinery dropped): sqlc, no
cross-schema FKs (cross-layer links are business-key `BIGINT`s), JSONB for non-queryable data,
single-column surrogate PKs, natural keys as composite `UNIQUE`, named constraints ≤63 bytes.

Schemas: **`ingest`** (file manifest + pipeline state) → **`bronze`** (raw files + license
provenance) → **`silver`** (frameworks/versions/documents/controls + relations + mappings) →
**`gold`** (chunks + vectors), plus **`config`** (the framework registry + vocabularies).
The separate **`auth`** schema holds operational OAuth state; it is not a medallion layer and is
never managed by `cmd/seed`.

## `auth` — persistent identities and OAuth grants

All authorization codes, client secrets, access tokens, and refresh tokens are persisted only as
SHA-256 hashes. Stable identity is `(provider, subject)`; normalized email is nullable profile data
reserved for a later verified-email policy and is not an identity key.

| Table | Role |
|-------|------|
| `identity` | Stable authenticated principal, with nullable `email_normalized` and a non-null `email_verified` flag (default false). |
| `oauth_client` | DCR/CIMD client metadata, hashed confidential-client secret, redirect URIs, activity timestamps, and CIMD metadata-refresh time. |
| `authorization_code` | Single-use PKCE grant bound to an identity and client; 10-minute expiry. |
| `token_family` | Identity/client/session binding and family-wide expiry/revocation boundary. |
| `access_token` | Hashed opaque access credential with sliding expiry and last-use time. |
| `refresh_token` | Hashed rotating refresh credential; consumed rows are retained for replay detection. A partial unique index allows one live refresh token per family. |

## `config` — the framework registry (seeded, operator-overridable)

The registry is data, not Go. CSVs in `deploy/seed/`; `origin='seed'|'user'` + `enabled` exactly as
banhmi (re-seed replaces seed rows, never touches user rows).

| Table | Role | Key columns | Unique |
|-------|------|-------------|--------|
| `framework` | One row per framework | `code` (`iso27001`, `soc2tsc`, `pcidss`, `nistcsf`, `nist80053`, `ciscontrols`, `iso27002`, `iso27017`, `iso27018`, `iso27701`, `iso22301`, `iso42001`, `swiftcscf`, `csaccm`, `cobit`, `tcvn11930`, `tcvn14423`) · `name` · `publisher` · `source_access` (`auto-fetch`/`form-gated`/`byo`) · `license_class` (`public-domain`/`open-restricted`/`licensed`) · **`ingest_enabled`** (default true — operator opt-out; the pipeline warns on frameworks whose terms restrict knowledge-base use, e.g. AICPA TSC, and the operator owns the choice) · **`terms_note`** (data-driven restricted-terms warning text, e.g. AICPA's knowledge-base clause; non-empty → pipeline prints the warning) · **`serve_policy`** (`full` / `auth-text-only` / `operator-assumes-risk`) · `citation_scheme` (parser + cite-format key) | `(code)` |
| `framework_version` | One row per published version | `framework_code` · `version_label` (`2022`, `v8.1.2`, `2.0`, `v4.0.1`) · `published_on` · `is_current` · `edition_note` | `(framework_code, version_label)` + partial unique `uq_config_framework_version_current` on `(framework_code) WHERE is_current` (at most one current per framework) |
| `mapping_source` | Provenance vocab for mapping edges | `code` (`nist-olir`, `csa-ccm-v4.1`, `cis-v8.1-mappings`, `publisher-catalog`, `iso-structural`, `operator`) · `name` · `authority_note` | `(code)` |
| `control_kind` | Vocab for `silver.control.kind` | `code` (`domain`/`family`/`clause`/`control`/`enhancement`/`criterion`/`point-of-focus`/`requirement`/`objective`/`practice`/`safeguard`/`annex-control`/`function`/`category`/`subcategory`) — framework-native unit naming; COBIT practices (`EDM01.01`) and TSC points of focus are first-class citable units, not body text | `(code)` |
| `reference_source` | Prefix→target mapping for informative-reference extraction | `prefix` (exact colon-split match, e.g. `SP 800-53 Rev 5.2.0`) · `to_framework_code` · `to_version_label` (nullable = version-unspecified) · `mapping_source_code` (default `publisher-catalog`) · `enabled` · UNIQUE `(prefix)` — seeded from CSV; unknown/disabled prefixes are skipped and counted, never guessed | `(prefix)` |
| `curated_mapping` | **Operator-authored cross-framework edges** for corpora that ship no crosswalk table (the TCVN corpora) | `from_framework_code`+`from_version_label`+`from_citation_norm` (business key resolved to a `silver.control`) · `to_framework_code`+`to_version_label` (nullable = unpinned)+`to_citation_norm` · `relationship` (same vocab as `silver.control_mapping`) · `mapping_source_code` (default `operator`) · `note` (our own honesty caveat, e.g. MOD-lineage version drift — never licensed text) · `origin` seed/user · UNIQUE NULLS NOT DISTINCT `(from_framework_code, from_version_label, from_citation_norm, to_framework_code, to_version_label, to_citation_norm, mapping_source_code)` — the mapedges stage resolves each row's from side and upserts a `silver.control_mapping` edge; rows whose from-control is absent are counted + reported, never dropped | `(from…, to…, mapping_source_code)` |
| `file_rule` | Filename→document matching + per-file license provenance | `ordinal` (match order; first match wins) · `pattern` (path.Match glob over rel_path) · `framework_code`/`version_label`/`doc_role`/`qualifier` (NULL for ignore rules) · `ignore`/`ignore_reason` · `file_format` · `license_kind`/`source_url`/`provenance_note` (provenance flows to `bronze.source_file` at extract time) · `origin` seed/user | `(pattern)` |
| `setting` | key/value gates | `key`, `value` | `(key)` |

Supersession is derived from `framework_version` ordering **plus** explicit
`silver.version_relation` rows (a version can be superseded by a doc outside simple ordering,
e.g. amendments).

## `ingest` — file manifest (the whole pipeline state)

No discovery, no leases. One table.

| Table | Role | Key columns |
|-------|------|-------------|
| `manifest_file` | One row per file under `data/` | `rel_path` UNIQUE · `sha256` · `size_bytes` · `framework_code` + `version_label` + `doc_role` + **`qualifier`** (matched from `config.file_rule`; NULL framework fields = unrecognized → quality gap) · `file_format` (`oscal-json`/`xlsx`/`pdf`) · **`ignored`** + `ignore_reason` (file_rule can mark files as non-framework) · **`status`** (`active`/`removed` — vanished/renamed path demoted, never deleted) · per-stage state: `extracted_at`/`normalized_at`/`indexed_at` + `stage_error` · re-run diff: state resets when `sha256` changes |

`doc_role` vocabulary includes **`guide`** (in-corpus, recorded in manifest, never extracted or
parsed — redundant renderings, methodology volumes, guidance PDFs). Per-file read/hash errors are
recorded in `stage_error` and the file continues; the summary counts failed files.

Scan-time ambiguity rule: two `active` files resolving to the same
`(framework_code, version_label, doc_role, qualifier, file_format)` — for **parse-eligible roles
only** (`main`, `amendment`, `companion-workbook`) — is an error; the scan reports both as a quality
gap and processes neither (prevents duplicate control trees). Guides, changelogs, and ignored files
are exempt.

Completeness = every `manifest_file` row for an `is_current` version reaching `indexed_at`,
recomputed by query — never a stored boolean. Pipeline stages iterate all eligible rows, record
per-row errors, and continue; `cmd/pipeline` exits non-zero with a N-succeeded/M-failed summary.
`stage_error` and quality-gap messages carry citations, paths, and line numbers — **never verbatim
document text** (log-leak rule).

## `bronze` — raw capture + license provenance

| Table | Role | Notes |
|-------|------|-------|
| `source_file` | One row per ingested file observation | `manifest_rel_path` · `sha256` · `framework_code`/`version_label` · **license provenance:** `source_url` (official publisher page), `license_kind` (verbatim class: `public-domain`/`cc-by-nc-nd`/`click-through`/`purchased`/`membership`/`unverified`), `retrieved_on`, `provenance_note` (e.g. "ITU X.1631 co-publication", "operator-accepted re-hosted copy") · **`serve_gate`** (`public`/`auth-only`) — the read path enforces this per document |
| `raw_extract` | Extracted raw structures per file | `kind` (`text-markdown`/`oscal-catalog-json`/`workbook-rows-json`/`pdf-pages-json`/`txt-pages-json`) · `content` / `content_jsonb` · UNIQUE `(source_file_id, kind)` (idempotent re-extract) |

**`workbook-rows-json` capture:** the canonical raw extraction for XLSX files — cell grid as
`{"sheets":[{"name":…,"rows":[{"ref":"A5","value":…}…]}…]}`, shared strings resolved, no styling.
XLSX is a binary container; byte-preservation does not apply — the JSON capture IS the raw
extraction (unlike PDF `text-markdown`, where the file itself is byte-preserved in `source_file`).

**`pdf-pages-json` capture:** the canonical raw extraction for PDF files — page-scoped text via
go-fitz (purego, no cgo): `{"pages":[{"n":1,"text":"…"},…]}`. Page numbers enable parser provenance
citations (`p.37`). Supersedes the earlier `text-markdown` intent for PDFs — `text-markdown` remains
valid for non-PDF text sources.

**`txt-pages-json` capture:** the canonical raw extraction for page-marked plain-text files — same
page shape as `pdf-pages-json` (`{"pages":[{"n":1,"text":"…"},…]}`). Pages are split on marker lines
of the exact form `===== [trang N] =====`; marker lines are stripped, N becomes the page number, and
all other whitespace is preserved verbatim (text before the first marker belongs to page 1).

## `silver` — frameworks, controls, versions, mappings

| Table | Role | Notes |
|-------|------|-------|
| `document` | One publication (framework version's document) | `doc_key` = `<framework_code>|<version_label>|<doc_role>[:<qualifier>]` — **amendments attach to the base version they modify** (`iso27001|2022|amendment:amd1-2024`), so the base linkage is explicit · `doc_role` (`main`/`amendment`/`companion-workbook`/`changelog`) · **`qualifier`** (explicit column backing the `doc_key` suffix, e.g. `amd1-2024`; empty for non-amendment docs) · title · `source_file_sha256` business key · **`serve_gate`** (`public`/`auth-only`, denormalized from `bronze.source_file`) · denormalized display `markdown` · **`mapedges_at`** (outbound-mapping-edge staleness marker, nullable — NULL = edges unbuilt or cascade-deleted by a re-normalize; see *Stage ordering* below) |
| `control` | **The citation unit** | `document_id` · `citation` UNIQUE per `(document_id, citation)` (`A.5.1`, `AC-2(3)`, `CC6.1`, `8.3.6`, `PR.AA-01`, `CLD.6.3.1`, `AIS-01`, `EDM01.01`, `5.1`; `tcvn-clause` dotted-numeric `5.1.1.1` + appendix `A.5.10` for TCVN) · `citation_norm` (uppercased, separator-normalized — the mapping join key; **scoped per framework, never globally unique** — 27017's 2013-numbered `6.1.1` ≠ 27001:2022's `6.1`) · `parent_control_id` (tree: domain → family → control → enhancement; objective → practice; criterion → point-of-focus) · `kind` (from `config.control_kind`) · **`status`** (`active`/`withdrawn`/`deprecated` — 800-53r5 has 182 withdrawn controls; incorporation edges go to `control_mapping` as `incorporated-into`) · **`title`** (always ours/paraphrased for `licensed` frameworks — public-safe by construction; may equal publisher's only for `public-domain`) · **`title_original`** (publisher's verbatim heading, nullable — auth-gated like `body`) · `body` (verbatim normative text — **auth-gated at serve time by the document's `serve_gate`**) · **`amends_citation_norm` + `amend_action`** (`add`/`replace`/`delete`, nullable — set only on controls inside amendment documents, linking each patch to the base control it modifies; a query for "current 27001 4.1" joins base + amendment deterministically) · `ordinal` |
| `version_relation` | Version lineage edges | `from_framework_code`+`from_version_label` → `to_framework_code`+`to_version_label` · `relation_type` (`supersedes`/`amends`/`consolidates`) · `note` (e.g. "cancels and replaces; realigned to 27002:2022") — populated from registry knowledge + publisher forewords · endpoints validated against `config.framework_version` at pipeline time; unresolved → quality gap |
| `control_mapping` | **Cross-framework edges, the product's second half** | `from_control_id` → `to_framework_code` + **`to_version_label`** (nullable = version-unspecified; the real data is version-specific — CSF 2.0's workbook maps separately to 800-53 r5.1.1 and r5.2.0) + `to_citation_norm` (business key — target may not be ingested yet, banhmi's `doc_ref` stub pattern flattened) · `mapping_source_code` (config vocab) · `relationship` (`equivalent`/`subset-of`/`superset-of`/`intersects`/`related`/`incorporated-into`/`moved-to`) · `provenance_detail` (row/cell reference only, source-specific format: OSCAL link href · `<sheet>!<cell>` for workbook rows · `<cell> [<prefix>]` for informative references — never quoted text) · resolved `to_control_id` nullable, filled when the target framework lands · candidate key `(from_control_id, to_framework_code, to_version_label, to_citation_norm, mapping_source_code)` |
| `control_topic` | Optional theme tags | deferred vocabulary; schema-ready |

**Informative-reference edges:** publisher workbooks (e.g. CSF 2.0 col E) list relatedness claims
to other frameworks. These are extracted as `control_mapping` with `relationship='related'`,
`mapping_source_code='publisher-catalog'`, source prefix resolved via `config.reference_source`.
Publisher citation typos (e.g. ISO `6.11`/`6.13`) are recorded **verbatim** — fidelity to source,
surfaced later as quality gaps by design. Version-unspecified targets (e.g. PCI DSS with no version
in the reference line) use `to_version_label=NULL`, resolving lazily to the current version when
that framework's parser lands.

**Operator-curated edges:** for corpora whose publications ship no crosswalk table (the TCVN
corpora), the mapedges stage resolves `config.curated_mapping` rows into `control_mapping` under
`mapping_source_code='operator'`. Each row is the operator's own analysis of both endpoints' text —
never publisher-authoritative; `provenance_detail` carries the operator note (e.g. MOD-lineage version
drift), not a source cell. From-controls absent from the corpus are counted + reported, never dropped.

**Stage ordering — re-run `-stage mapedges` after any normalize.** Re-normalizing a framework
deletes and recreates its `silver.control` rows, and `control_mapping.from_control_id` is
`ON DELETE CASCADE`, so every mapping edge leaving that framework is dropped. `mapedges` is **not**
in `cmd/pipeline`'s default stage list (adding it would turn a scoped normalize into a whole-corpus
rebuild), so a normalize-only run leaves curated and derived edges missing until mapedges runs again.

**`silver.document.mapedges_at` is the staleness marker** — the silver twin of
`ingest.manifest_file.indexed_at`, making the loss queryable state rather than a log line:

| Step | Behavior |
|------|----------|
| **Grain** | Per **document** — the exact unit the cascade destroys (`DeleteControlsForDocument` → CASCADE on every edge leaving that document) |
| **Invalidated** | `DeleteControlsForDocument` NULLs it in the **same statement** (data-modifying CTE), so a rebuilt control tree can never leave a stale-but-fresh marker |
| **Set** | `normalize.MarkMapEdgesFresh` stamps **every** document after the whole mapedges stage succeeds — the stage has no per-framework mode, so corpus-wide is the honest grain; a partial/failed run marks nothing |
| **Surfaced** | `quality_gaps category=stale_mapedges` lists every NULL with its control + surviving-edge counts, distinguishing *never run* (cold corpus) from *re-normalized since the last run* (regression) |
| **Warned** | The normalize stage logs a **WARN** naming the doc, `edges_deleted`, `edges_reemitted_by_parser`, and `needs_mapedges` — the moment the operator can act |

`quality_gaps category=curated_mappings` remains the complementary check: it diffs seeded
`config.curated_mapping` rows against materialized edges (39 of 4,501), while the marker covers
**derived** edges (`iso-structural`, `nist-olir`, `cis-v8.1-mappings`, `publisher-catalog`), which
have no seed table to diff against.

**Withdrawn-in-document modeling:** when a publisher lists superseded items inline (e.g. CSF v1.1
categories/subcategories inside the 2.0 workbook, marked `[Withdrawn: Incorporated into …]`),
they live in the **2.0 document** — never a fabricated v1.1 document we don't have. Status
`withdrawn`, `incorporated-into`/`moved-to` edges to 2.0 targets.

**Mapping resolution** (`to_control_id` fill) runs in three passes during the mapedges stage:

1. **Standard resolve:** exact `citation_norm` match against the target framework's version
   (version-pinned edges match that version's document; version-unspecified edges match the current
   version via `config.framework_version.is_current`).
2. **Annex-A prefix normalization (iso27001 only):** edges citing bare Annex numbers (`5.26`) are
   resolved against corpus controls with the `A.` prefix (`A.5.26`), but **only when** the bare
   form does not already match a management clause (`kind='clause'`). ISO 27001:2022 has management
   clauses 4-10 (bare numbers) and Annex A controls A.5-A.8; publisher workbooks (OLIR, CIS
   mappings) cite Annex controls without the `A.` prefix. The guard prevents mis-resolving a
   management clause to an Annex control. Provenance appends `annex-prefix: resolved X as A.X`.
3. **Cross-version supersession:** edges citing a version that has an explicit
   `silver.version_relation(supersedes)` row connecting it to an ingested successor resolve against
   the successor's controls. Single-hop only (v8 -> v8.1, v4.0 -> v4.1). The edge **keeps its
   original `to_version_label`** (fidelity to source); provenance appends
   `version-supersession: cited <old> resolved in successor <new>`. Current relations:
   CIS Controls v8.1 supersedes v8, CSA CCM v4.1 supersedes v4.0.

Genuinely-missing targets (e.g. nistcsf 1.1, CCM IVS-* removed in v4.1, ISO citation typos like
`6.11`/`6.13`) stay unresolved — never forced.

Key differences from banhmi's silver: no gazette/alias/text-authority machinery (single
authoritative file per document, no reconcile); validity collapses into `version_relation` +
`framework_version.is_current` (frameworks don't get partially repealed — a version is current or
superseded; amendments are `amends` edges with their own document).

## `gold` — chunks + vectors (retrieval design: [`RETRIEVAL.md`](RETRIEVAL.md))

| Table | Role | Notes |
|-------|------|-------|
| `chunk` | Control-level chunks | `control_id` · `citation` (display cite: `ISO/IEC 27001:2022 A.5.1`, `NIST SP 800-53r5 AC-2(3)`) · `context_prefix` (framework + version + family/clause path + **paraphrased** `title` only — never `title_original`) · `content` · `content_sparse` (`sparsevec` BM25) · UNIQUE `(control_id, ordinal)` (one chunk per control today; ordinal reserved for deep controls like COBIT objectives, no migration later) |

**Chunking granularity for sub-control kinds is a per-framework parser decision, settled by eval:**
points of focus and practices are `control` rows (citable, mappable), but whether they chunk
individually or fold into their parent criterion/objective's chunk is decided when that parser
lands, with retrieval eval evidence — not fixed here.
| `chunk_embedding` | Dense vectors | Qwen3-Embedding 1024-d · one row per `(chunk_id, model)` · exact cosine scan (no HNSW; re-evaluate at 10k+ chunks) |

Serve-time rule: unauthenticated callers get citations, **paraphrased titles**, scores, and mapping
edges for everything; chunk `content`, `body`, `title_original`, and `context_prefix` for
`auth-only` documents are returned only past auth — a public caller gets the citation + a gap
notice, never licensed text. (The maintainer instance is auth-only anyway; this rule is the safe
default for every self-deployed operator.)

## Parsing order (M2 build order)

1. **NIST SP 800-53 r5 — OSCAL JSON** (`nist-sp-800-53r5-oscal-catalog.json`): structured catalog →
   `control` tree with enhancements. Richest, zero PDF risk. Proves the whole schema. **Landed.**
2. **NIST CSF 2.0 — XLSX** (`nist-csf-2.0.xlsx`): Function.Category.Subcategory rows. **Landed.**
3. **CIS Controls v8.1 — XLSX**: 18 controls + 153 safeguards = 171 rows (asset class, security
   function, IG columns as labeled body lines). `serve_gate` public (CC BY-NC-ND). **Landed.**
4. **CSA CCM v4.1 — XLSX**: 17 domains + 207 controls = 224 rows. `serve_gate` auth-only.
   **Landed** (control tree only). **Title-as-heading policy:** licensed-framework headings are
   citable metadata (CSA publishes them freely in every public CCM index); `title` = publisher
   heading, `body` (Control Specification) auth-gated. **Mappings deferred:** CSA's v4.1 workbook
   Scope Applicability sheet contains only "This dataset is not available yet" — no mapping edges
   this round; the `csa-ccm-v4.1` mapping source stays unused. **CAIQ deferred:** the normalize
   dispatch skips non-`main` doc roles (e.g. `companion-workbook` for CAIQ sheets) — assessment
   questions are not controls.
5. **PCI DSS v4.0.1 — PDF**: 15 roots (Requirements 1–12 + A1/A2/A3) + 351 numbered requirements
   = 366 rows; depth distribution X.Y=71 / X.Y.Z=230 / X.Y.Z.W=49 / depth-5=1. **Landed.**
   Titling: generated neutral labels (`"Requirement 8.3.6"`), `title_original` NULL — licensed
   no-title framework rule (see title policy in silver `control` above). Testing Procedures +
   Guidance columns not modeled (assessment machinery). **Body cleanliness:** the normalizer
   truncates each body at the column boundary (`rePCIStopLine` — "Defined Approach Testing
   Procedures" / trailing "Guidance" headers), so bodies carry only Defined Approach Requirement
   text; the 2026-07-20 audit shows 0/351 bodies with column noise (was 282/351 before the
   stop-line pass).
6. **AICPA TSC — PDF**: 61 criteria (CC/A/C/PI/P series) + 332 points of focus = 393 rows; neutral
   titles (`"Criterion CC1.1"`), `title_original` = PoF bold lead-in (auth-gated), `serve_gate`
   auth-only. `terms_note` warning fires at normalize (AICPA knowledge-base clause). **Landed.**
7. **ISO family — PDF**: 27001:2022 = 138 rows (45 clauses + 93 Annex A); 27002:2022 = 97 (4 themes
   + 93 controls); 27017:2015 = 176 (incl. 7 CLD cloud-extended); 27018:2019 = 120 (incl. 25
   Annex A PII). Neutral titles (`"Annex A control A.5.1"`), `title_original` auth-gated or NULL,
   `serve_gate` auth-only. **Landed.** Deferred: 27001 Annex A bodies are table-shallow (go-fitz
   renders minimal text from the reference table); amendments (27001+22301 amd1-2024) role-guarded,
   not normalized this round.
8. **COBIT 2019 — PDF**: 5 domains + 40 objectives + 231 practices = 276 rows; neutral titles
   (`"Practice EDM01.01"`), `title_original` auth-gated, `serve_gate` auth-only. **Landed.**
9. **TCVN 11930:2017 + TCVN 14423:2025 — Vietnamese**: `tcvn-clause` scheme (dotted-numeric
   clauses + `A.` appendix). 11930 = 290 rows (65 clauses + 225 controls); 14423 = 249 rows
   (67 clauses + 182 controls). `title` = **our own Vietnamese paraphrase** for all 407 controls
   (public-safe by construction); clause nodes keep the publisher heading in `title_original`
   (auth-gated). `serve_gate` auth-only (licensed — VSQI copyright). **Landed.**

Each parser lands with golden-count tests (expected control counts per version — e.g. 27001:2022
Annex A = 93, CCM v4.1 = 207 controls + 17 domains, CSF 2.0 subcategories = 106) — parsed counts
must match exactly.

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
