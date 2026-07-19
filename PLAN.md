# compliary plan

Living roadmap and progress tracker. Conventions and the canonical agent guide in
[`CLAUDE.md`](CLAUDE.md). Last updated: 2026-07-19.

## Vision

An **open-source, self-deployable evidence pipeline + MCP server** for the **Information Security &
Cybersecurity control frameworks** organizations are audited against — **Vietnam-relevant first** —
serving exact control citations, version lineage, cross-framework mappings, provenance, and explicit
gaps.

- **Operators build their own corpus** under licenses they accepted and run their own instance; the
  repo never ships licensed document text. The maintainer's instance at **`compliary.danny.vn`** is
  **maintainer-only** — public landing page for project info, `/mcp` authenticated for the
  maintainer alone, never serving other users.
- **One corpus, one DB.** Framework is a registry dimension, not a deployment
  (unlike banhmi's corpus-per-country).
- **The data is the product; the user brings the model.** No built-in answer LLM.
- **Positioning vs banhmi:** banhmi = binding law by jurisdiction; compliary = voluntary/contractual
  frameworks & standards. VN regulations (e.g. Decree 85/2016 security levels, SBV circulars) stay in
  banhmi; cross-product links are future work.

> **Status convention:** "coded" = code written + unit/integration tests; "validated" = checked on
> real documents. Never report one as the other.

## Scope — frameworks

The frameworks the Vietnamese market (banks, fintech, SaaS, BPO/ITO) is actually certified or
assessed against. Unlike regulations, these are **few, stable documents** (~15 frameworks, ~25
files). **v0.1.0 builds on the 12 frameworks acquired; the 5 phase-2 documents land in v0.2.0.**

| # | Framework | Current version | Publisher | Access / license | Ingestion | Citation unit |
|---|-----------|-----------------|-----------|------------------|-------------|---------------|
| 1 | ISO/IEC 27001 | 2022 + Amd 1:2024 | ISO/IEC JTC 1/SC 27 | paid, copyrighted | BYO (purchase) | Clause; Annex A control (A.5.1–A.8.34) |
| 2 | SOC 2 (TSC) | 2017 TSC, 2022 Revised Points of Focus | AICPA | copyrighted | BYO (free download) | Criteria (CC1.1–CC9.2; A/C/PI/P series) |
| 3 | PCI DSS | v4.0.1 (2024-06; mandatory 2025-03-31) | PCI SSC | free download, restricted license | BYO (click-through) | Requirement (1–12, e.g. 8.3.6) |
| 4 | NIST CSF | 2.0 (2024-02) | NIST | public domain | auto-fetch | Function.Category.Subcategory (PR.AA-01) |
| 5 | NIST SP 800-53 | Rev 5 | NIST | public domain, OSCAL available | auto-fetch (OSCAL) | Control + enhancement (AC-2(3)); 20 families |
| 6 | CIS Controls | v8.1 (docs at v8.1.2, 2025-03) | CIS | free, CC BY-NC-ND 4.0 | auto-fetch | Control / Safeguard (5.1) |
| 7 | ISO/IEC 27002 | 2022 | ISO/IEC JTC 1/SC 27 | paid, copyrighted | BYO (purchase) | Control (5.1–8.34) |
| 8 | ISO/IEC 27017 | 2015 | ISO/IEC JTC 1/SC 27 | paid, copyrighted | BYO (purchase) | Control (27002:2013 numbering + CLD.x.x) |
| 9 | ISO/IEC 27018 | 2025 | ISO/IEC JTC 1/SC 27 | paid, copyrighted | BYO (purchase) | Control (27002:2022 numbering + Annex A PII) |
| 10 | ISO/IEC 27701 | 2025 (standalone PIMS; 2019 transition ends 2028) | ISO/IEC JTC 1/SC 27 | paid, copyrighted | BYO (purchase) | Clause; control |
| 11 | ISO 22301 | 2019 + Amd 1:2024 | ISO/TC 292 | paid, copyrighted | BYO (purchase) | Clause (4–10) |
| 12 | ISO/IEC 42001 | 2023 | ISO/IEC JTC 1/SC 42 | paid, copyrighted | BYO (purchase) | Clause; Annex A control |
| 13 | SWIFT CSCF | v2026 | SWIFT | members only | BYO (member access) | Control (1.1–7.x) |
| 14 | CSA CCM | v4.1 (2026-01) | CSA | free w/ registration | BYO (registration) | Control ID (AIS-01…) |
| 15 | COBIT | 2019 | ISACA | paid | BYO (purchase) | Objective (EDM01–MEA04) |

- **SOC 2 Type 1 vs Type 2** are *report types* (design vs operating effectiveness over a period),
  not separate criteria sets — one corpus entry over the TSC, report-type as metadata.
- **ISO cloud companion 27017 lags the 2022 renumbering:** still keyed to 27002:2013 control
  numbering (27018:2025 realigned to 27002:2022) — version-lineage relations must model
  cross-edition references.
- **Parser build order:** see [`docs/design/SCHEMA.md`](docs/design/SCHEMA.md) (OSCAL → XLSX → PDF).

**License gates (verified against live publisher sources 2026-07-19):**

- **NIST** — public domain (17 U.S.C. §105); auto-fetch and verbatim serving unrestricted.
- **CIS Controls** — CC BY-NC-ND 4.0 (Benchmarks: CC BY-NC-SA 4.0); registration-gated download.
- **PCI DSS** — click-through license: internal use + employee study only; no sublicense, no
  modification, no derivative works.
- **ISO** — all copying/distribution prohibited without written permission.
- **AICPA TSC** — personal non-commercial use only; explicit objection to LLM/AI knowledge-base
  inclusion — most conservative source.

**Acquisition status (2026-07-19): 12 of 15 frameworks have documents in `data/`.**
Remaining acquisitions are **phase 2 → v0.2.0** — v0.1.0 builds on what's in hand:

- **ISO 22301:2019 base** — purchase (Amd 1:2024 already in `data/iso/`, ISO-produced, parseable).
- **ISO/IEC 27018:2025** — purchase (interim :2019 in `data/iso/`, ingest flagged superseded).
- **ISO/IEC 27701:2025** — purchase.
- **ISO/IEC 42001:2023** — purchase.
- **SWIFT CSCF v2026** — reported free on swift.com (site in maintenance 2026-07-19); verify the
  gate when live, update the registry row, then drop into `data/swift/`.

Known data debts: none blocking. `data/iso/iso-iec-27002-2022.pdf` provenance noted in its data
commit (operator-accepted); 22301 has no parseable base text until the phase-2 purchase (no OCR —
normative text is never OCR-reconstructed).

**Later candidates (demand-driven):** NIST SP 800-171 r3 + CMMC 2.0, HITRUST CSF, OWASP ASVS 5.0 /
SAMM, CIS Benchmarks (per-technology hardening — a much larger corpus), NIST AI RMF 1.0,
ACSC Essential Eight, UK Cyber Essentials, SOC 1 / SOC 3, FedRAMP Rev 5 baselines.

**Mapping data sources (cross-framework relations):** NIST OLIR, CIS↔ISO/NIST mappings, CSA CCM
mappings, Secure Controls Framework (license check needed before use).

## Open decisions

1. **Maintainer `/mcp` auth mechanism** — bearer token + CloudFront origin header (banhmi
   pattern) vs OAuth (needed for claude.ai custom connectors). Decide at M4 (deploy).

## Roadmap

### v0.1.0 — corpus + serve (12 acquired frameworks)

Design settled in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) +
[`docs/design/SCHEMA.md`](docs/design/SCHEMA.md); M0 (bootstrap) and M1 (`cmd/fetch`) done — see
milestone history.

1. **M2 — parse + index** (in progress): ~~schema layer~~ done — ~~manifest scanner~~ done (26
   files) — ~~OSCAL extract~~ done — ~~XLSX extract~~ done (4 workbooks captured as
   `workbook-rows-json`) — ~~PDF extract~~ done (9 PDFs captured as `pdf-pages-json` via go-fitz
   purego) — ~~all 8 v0.1.0 parsers~~ done (11 documents / 3402 controls / 3068 edges / 1870
   resolved) — ~~Index + LexIndex~~ done (3402 chunks embedded + BM25 sparse) — ~~hybrid
   retriever~~ done (baseline: recall 57.8%, MRR 32.3%, current 100%). All acquired frameworks
   parse and retrieve. Deferred: amendments (27001+22301 amd1-2024) role-guarded; CAIQ; 27001
   Annex A bodies table-shallow; column-separation (PCI body noise); retrieval tuning.
   **Next:** MCP service (M3).
2. **M3 — MCP evidence service** — `guide`, `corpus_status`, `quality_gaps`, `search`, `document`;
   citation-keyed golden set + eval gate with baseline floors.
3. **M4 — deploy maintainer instance** — `compliary.danny.vn`: public landing, **authenticated
   `/mcp`** (auth per open decision 1). Reuse banhmi's AWS shape (CloudFront → ECS → RDS).

### v0.1.x — patch releases

Improvements to the shipped v0.1.0 without new frameworks: parser fixes, retrieval-quality tuning,
eval-floor raises, doc/metadata corrections, dependency bumps. Corpus additions never land in a
patch — new documents always cut v0.2.0+.

### v0.2.0 — phase-2 corpus completion

1. Acquire the 5 deferred documents (see acquisition status above): ISO 22301:2019 base,
   ISO/IEC 27018:2025, 27701:2025, 42001:2023 (purchases; identical-text national adoptions like
   EVS are the cheap legitimate route), SWIFT CSCF v2026 (verify gate when swift.com is live).
2. Ingest them: 27018:2025 lands **alongside** :2019 (supersession chain `2014→2019→2025`, 2019
   served flagged superseded); 22301 base text joins its already-ingested Amd 1:2024; 27701/42001/
   CSCF are new framework corpora.
3. Replace operator-flagged interim copies where purchases upgrade provenance (27002, 22301).

## Decisions (settled)

| Decision | Choice |
|----------|--------|
| Evidence-only; no answer LLM | citations/versions/mappings/gaps over MCP; user brings the model |
| Distribution | open-source self-deploy; repo ships code + metadata seeds, never licensed text |
| Ingestion | auto-fetch public-domain (NIST) only; license-gated sources are operator-BYO |
| Private serving | licensed text only behind auth ("internal use"); maintainer `/mcp` authenticated |
| One corpus, one DB | framework = registry dimension; maintainer instance at `compliary.danny.vn` |
| Corpus language | English (publication language); VN focus selects frameworks, never translates |
| Stack | Go + PostgreSQL/pgvector + sqlc + hybrid retrieval + MCP (banhmi-proven) |
| Sources | official publisher sources only; license provenance per document |
| Embedder | maintainer deploy **shares banhmi's embedder** (wiring at M4); self-deploy ships embed/lexindex/retrieve code **copied from banhmi** (same author) at the Index stage |

## Milestone history

- **2026-07-19** — **M0 + M1 + corpus + M2 design + M2 schema layer.** Bootstrap (guide, plan,
  signed git); license gates verified against live publisher sources (verdicts above). `cmd/fetch`
  shipped + validated live (NIST/PCI/CIS). Corpus acquisition: 12/15 frameworks in `data/`,
  filenames kebab-cased (fetchers aligned, re-run validated), versions verified (27018 → :2025,
  22301 + Amd 1:2024, CCM → v4.1); 5 documents deferred to phase 2 (v0.2.0). M2 design docs
  written, two-agent review passed, fixes folded in; design questions 1–3 (schema, registry,
  reuse) settled in `docs/`. M2 schema layer landed: 5 PG schemas (`config`/`ingest`/`bronze`/
  `silver`/`gold`) + sqlc stores, Atlas→goose migrations, seeded registry (15 frameworks /
  28 versions / 12 control kinds / 5 mapping sources) validated against local Postgres;
  `cmd/migrate` + `cmd/seed`, Makefile, `deploy/compose/compliary.yaml`.
- **2026-07-19** — **M2 manifest + extract + normalize (NIST SP 800-53 r5).** `config.file_rule`
  registry (26 rules) seeded; manifest scanner classifies all 26 `data/` files (23 matched /
  3 ignored). OSCAL JSON extract into `bronze.source_file` with file_rule-sourced license provenance
  + `serve_gate`. NIST 800-53 r5 normalized to silver: 20 families, 324 controls,
  872 enhancements = 1216 rows; 182 withdrawn; 200 publisher-catalog mapping edges (166
  incorporated-into + 34 moved-to) resolved via `ResolveControlMappings`; golden-count tests
  pinned; idempotent delete-and-rebuild. `cmd/pipeline` + `pkg/manifest` + `pkg/extract` +
  `pkg/normalize` landed. XLSX/PDF extractors deferred (next parser wave: CSF 2.0 XLSX).
- **2026-07-19** — **M2 XLSX extract + CSF 2.0 tree + informative-reference mappings.** XLSX
  extractor: 4 workbooks captured into bronze as `workbook-rows-json` (canonical capture; PDF
  deferral now 9). CSF 2.0 normalized: 6 functions + 34 categories (22 active / 12 withdrawn) +
  185 subcategories (106 active / 79 withdrawn) = 225 rows, 91 withdrawn; implementation examples
  appended to active subcategory bodies; 136 intra-CSF withdrawal edges (117 incorporated-into +
  19 moved-to); `control_kind` vocab gained `function`/`category`/`subcategory` (15 kinds total).
  `config.reference_source` seeded: 8 prefix→target mappings; CSF informative-reference edges:
  2732 `related` edges — nist80053/r5 747 (all resolved), csaccm/v4.0 657, pcidss/NULL 551,
  iso27001/2022 470, nistcsf/1.1 185, ciscontrols v8.1 62 + v8 60; 800-53 dual-release lines
  dedupe to r5; publisher typos recorded verbatim (surfaced as quality gaps). Corpus totals: 26
  manifest / 5 bronze / 1441 silver controls / 3068 mapping edges (1083 resolved).
- **2026-07-19** — **M2 CIS Controls v8.1 + CSA CCM v4.1 — parallel parser wave.** Shared
  `writeTree` helper extracted (normalizeOSCAL/normalizeCSF become thin adapters); `CaptureXLSXFile`
  exported for golden tests. CIS v8.1 normalized: 18 controls + 153 safeguards = 171 rows (kinds
  control/safeguard; IG, asset class, security function as labeled body lines; `serve_gate` public).
  CCM v4.1 normalized: 17 domains + 207 controls = 224 rows (kinds domain/control; applicability as
  labeled body lines; `serve_gate` auth-only; title-as-heading policy — licensed headings are citable
  metadata). CSF's 62 v8.1-pinned CIS edges ALL resolved (62/62) — first cross-framework resolution
  proof. CCM v4.1 mappings deferred (CSA ships "not available yet"); CAIQ deferred (non-main doc role
  skipped by normalize dispatch). Corpus totals: 4 documents, 1836 controls, 3068 edges (1145
  resolved: 947 nist80053 + 136 nistcsf + 62 ciscontrols). Unresolved: CIS v8 60 + CCM v4.0 657 +
  ISO 470 + PCI 551 + CSF v1.1 185 — pending parsers/documents.
- **2026-07-19** — **M2 PDF extraction + PCI DSS v4.0.1 parser.** PDF extraction landed: go-fitz
  v1.28.2 (purego, no cgo); bronze kind `pdf-pages-json` (page-scoped text capture, supersedes
  `text-markdown` intent for PDFs); all 9 eligible PDFs captured; extract deferrals now zero.
  PCI DSS v4.0.1 normalized: 15 roots (Requirements 1–12 + A1/A2/A3) + 351 numbered requirements =
  366 rows; depth X.Y=71 / X.Y.Z=230 / X.Y.Z.W=49 / depth-5=1; titles are generated neutral labels
  (`"Requirement 8.3.6"`), `title_original` NULL (licensed no-title framework rule); Testing
  Procedures + Guidance columns deferred (assessment machinery). Body noise: go-fitz 3-column
  interleave leaks guidance prose into 282/351 bodies after the requirement text (noisy, not wrong;
  column-separation pass deferred to eval). Controller audit caught a same-number testing-procedure
  collision that initially dropped requirement 10.2.1.4 — fixed via principled pre-scan + sibling/
  bracket gate recovery; synthetic fixture covers the collision shape. All 551 version-unspecified
  PCI edges from CSF now resolve (551/551) via the NULL-version→current-version arm. Corpus totals:
  5 documents / 2202 controls / 3068 edges / 1696 resolved.
- **2026-07-19** — **M2 final parser wave — TSC + ISO family + COBIT (three parallel worktrees).**
  AICPA TSC normalized: 61 criteria (CC/A/C/PI/P series) + 332 points of focus = 393 rows; neutral
  titles, `title_original` auth-gated, `serve_gate` auth-only; `terms_note` warning fires (AICPA
  knowledge-base clause). ISO family normalized: 27001:2022 = 138 (45 clauses + 93 Annex A);
  27002:2022 = 97 (4 themes + 93 controls); 27017:2015 = 176 (incl. 7 CLD cloud-extended);
  27018:2019 = 120 (incl. 25 Annex A PII); neutral titles everywhere, `title_original` auth-gated
  or NULL, `serve_gate` auth-only. COBIT 2019 normalized: 5 domains + 40 objectives + 231 practices
  = 276 rows; neutral titles, `title_original` auth-gated, `serve_gate` auth-only. New mapping
  resolutions: CSF→ISO 27001 174/470 (misses are citation-form mismatches — surfaced as quality
  gaps); CSF→PCI 551/551 and CSF→CIS 62/62 unchanged. ISO review caught + fixed a fixture licensing
  violation (real headings replaced with invented wording). Normalize now warns on non-empty
  `terms_note` (fires for soc2tsc — AICPA clause). Deferred: amendments (27001+22301 amd1-2024)
  role-guarded; CAIQ; 27001 Annex A bodies table-shallow. All v0.1.0 parsers landed. Corpus totals:
  11 documents / 3402 controls / 3068 edges / 1870 resolved.
- **2026-07-20** — **M2 Index round: hybrid retriever + first retrieval baseline.** Eval harness +
  golden set (50 queries, 10 citation schemes) landed earlier; Index + LexIndex stages landed
  (3402 chunks, 3402 embeddings, 3402 BM25 sparse vectors). Hybrid retriever (`pkg/rag/retrieve`):
  RRF fusion (dense cosine exact scan + BM25 sparsevec), version filters via
  `config.framework_version.is_current`, citation-shaped query routing (10 scheme regexes, direct
  `citation_norm` lookup with pinned hits), non-current version pass, status='active' filter on
  both arms (excludes 273 withdrawn controls). Tuned constants (v2): top_k=8, vector_k=50,
  bm25_k=50, rrf_k=20, lex_weight=0.5, doc_cap=0. Dropped from banhmi: VN diacritics, rollup,
  section-aggregate, HNSW, abbreviation expansion, identifier scope. Tuned baseline v2
  (hybrid, ONNX Qwen3 query-time, 50 queries): **recall@8 57.8%, MRR@8 34.1%, current-version
  100%, abstention 90%**. Eval floors: recall 0.55, MRR 0.32, current 0.98, abstain 0.88. Known
  gaps: no score-floor abstention (5 OOS cases return hits), short-chunk frameworks (ISO/SOC2/PCI)
  weak in both arms. Ancestor-title content enrichment tried and reverted (net-negative).
