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

None. (Auth settled 2026-07-20: **OAuth** — see Decisions.)

## Roadmap

### v0.1.0 — corpus + serve (12 acquired frameworks)

Design settled in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) +
[`docs/design/SCHEMA.md`](docs/design/SCHEMA.md); M0 (bootstrap) and M1 (`cmd/fetch`) done — see
milestone history.

1. **M2 — parse + index** (done): schema layer, manifest scanner (26 files), OSCAL + XLSX + PDF
   extract, all 8 v0.1.0 parsers (11 docs / 3402 controls / 3068 edges / 1870 resolved), Index +
   LexIndex (3402 chunks embedded + BM25 sparse), hybrid retriever (golden v2 baseline: recall
   63.3%, MRR 43.2%, current 100%, abstain 95.1%, 105 cases). Deferred: amendments, CAIQ, 27001
   Annex A bodies, PCI column-separation, retrieval tuning.
2. **M3 — MCP evidence service** (done): five tools (`guide`, `corpus_status`, `quality_gaps`,
   `search`, `document`) over the query core (`pkg/mcp`). Transports: stdio (`cmd/mcp`, full
   projection) + Streamable HTTP (`cmd/server`, bearer-token auth boundary). ISO-family structural
   equivalence edges (186 bidirectional, all resolved, `iso-structural` mapping source). Score-floor
   abstention wired (floor=0 — score band too compressed at 3.4k chunks for OOS separation;
   operator-tunable). Haiku stand-in agent validated tool contract end-to-end over real stdio.
   Eval (ONNX, 105 cases): open-corpus recall 65.0%/MRR 44.6%/current 100%/abstain 95.2%;
   filtered recall 80.0%/MRR 62.9%/current 94.2%/abstain 95.2%. No regression vs Phase A baseline.
   Tool contract in [`docs/design/MCP.md`](docs/design/MCP.md).
3. **M4 — deploy maintainer instance** (**live + validated 2026-07-20**) — `compliary.danny.vn`:
   **authenticated `/mcp`** (OAuth; unauthenticated → 401). Co-located as **one extra ECS service on
   banhmi's EC2 host** (cluster `banhmi`), calling banhmi's embedder over loopback
   (`COMPLIARY_EMBED_ENDPOINT=http://127.0.0.1:8089`) so only the slim CGO-free MCP server ships (no
   ONNX). Origin-verify (`X-Origin-Verify`) enforces CloudFront-only ingress; the corpus lives in a
   `compliary` database on banhmi's shared RDS. **Validated end-to-end through CloudFront:** health
   200, unauth `/mcp` 401, OAuth metadata, authenticated MCP handshake, `corpus_status` (11
   frameworks / 3402 controls) and `search` returning correct evidence (PCI DSS MFA → Req 8.4/8.5/
   8.3.11) — exercising the shared embedder. Files:
   [`deploy/containerfiles/Containerfile.ecs.server`](deploy/containerfiles/Containerfile.ecs.server),
   [`deploy/aws/`](deploy/aws/) (task def, CloudFront, `setup-checklist.md`); topology in
   [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

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
| Private serving | licensed text only behind auth ("internal use"); maintainer `/mcp` authenticated. **M3 contract: authenticated + local-stdio callers get FULL VERBATIM text (body, title_original, chunk content); the paraphrased-only reduced surface applies solely to unauthenticated HTTP** |
| One corpus, one DB | framework = registry dimension; maintainer instance at `compliary.danny.vn` |
| Corpus language | English (publication language); VN focus selects frameworks, never translates |
| Stack | Go + PostgreSQL/pgvector + sqlc + hybrid retrieval + MCP (banhmi-proven) |
| Sources | official publisher sources only; license provenance per document |
| Maintainer `/mcp` auth | **OAuth** (MCP auth spec: authorization-code + PKCE + dynamic client registration) so the instance connects as a **claude.ai and chatgpt.com custom connector**; **unauthenticated requests get 401 for everything** (no anonymous reduced surface on the maintainer instance — projection layer stays as defense-in-depth; self-deployers may opt into a public metadata surface) |
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
  section-aggregate, HNSW, abbreviation expansion, identifier scope. Ancestor-title content
  enrichment tried and reverted (net-negative).
- **2026-07-20** — **Golden set v2: 105 adversarially-verified cases; baseline re-measured.**
  63 new cases (semantic, citation-shaped, version-pin, hard-paraphrase, cross-framework traps,
  hierarchy, withdrawn-control) merged with 42 v1 survivors (8 dropped: 1 defective, 7 dups).
  2 withdrawn-control cases (SC-19, ID.GV) marked expect_fail — retriever status=active filter
  excludes them (honest unreachability record). Corpus-citations snapshot extended to include
  withdrawn controls (273 rows added, citations-only metadata). Golden v2 baseline (hybrid,
  ONNX Qwen3, 105 queries / 98 scored): recall@8 63.3%, MRR@8 43.2%, current-version 100%,
  abstention 95.1%.
- **2026-07-20** — **Phase A retrieval improvements.** Golden label fixes (27002 5.7/5.11
  verified against DB bodies, 2 false negatives eliminated). CCM ampersand citation routing
  (A&A-01/I&S-05 forms now matched). ISO 27002 attribute-table boilerplate stripped (92/93
  control bodies cleaned, ~250 chars/control of identical prefix removed, re-embedded via
  Kaggle). `include_withdrawn` SearchOpts flag wired through both retrieval arms + citation
  lookup. Two-lane eval: open-corpus (no pins, floors gate) + framework-filtered (pins from
  golden metadata, `include_withdrawn` for withdrawn-target cases). 2 formerly `expect_fail`
  withdrawn cases (SC-19, ID.GV) un-expect_failed — pass in filtered lane via
  `include_withdrawn`. **Phase A baseline** (hybrid, ONNX Qwen3, 105 queries / 100 scored):
  **Open-corpus: recall@8 65.0%, MRR@8 43.1%, current 100%, abstain 95.2%.
  Filtered: recall@8 81.0%, MRR@8 62.5%, current 94.2%, abstain 95.2%.**
  Eval floors (open-corpus): recall 0.63, MRR 0.41, current 0.98, abstain 0.93. Remaining
  gaps: ISO 27018 3/4 pin cases still fail (superseded version structural); ISO 27001 Annex A
  short one-liners; PCI column interleave; semantic paraphrase misses.
- **2026-07-20** — **M3 MCP evidence service landed.** Five tools (`guide`, `corpus_status`,
  `quality_gaps`, `search`, `document`) in `pkg/mcp`; DB-backed query core over the retriever +
  silver/gold stores. Transports: stdio (`cmd/mcp`, full projection always) + Streamable HTTP
  (`cmd/server`, bearer-token auth, reduced projection when unauthenticated). ISO-family structural
  equivalence edges: 186 bidirectional `equivalent` edges (27001:2022 A.x.y to 27002:2022 x.y, 93
  pairs), mapping source `iso-structural`, all resolved; 27017/27018 intentionally omitted
  (27002:2013 numbering). Score-floor abstention: empirically derived floor=0 (score band too
  compressed at 3.4k chunks for OOS/in-scope separation; `search_abstain_floor` config setting
  seeded, operator-tunable). Haiku stand-in agent drove the real stdio server end-to-end with no
  repo access — tool contract validated for real compliance work (4 tasks: PCI MFA search, CSF
  PR.AA-01 mapping traversal, ISO 27001 A.5.1 currency + 27002 equivalent, GDPR out-of-scope).
  Eval (ONNX, both lanes): open-corpus recall 65.0%/MRR 44.6%/current 100%/abstain 95.2%;
  filtered recall 80.0%/MRR 62.9%/current 94.2%/abstain 95.2%. No regression vs Phase A baseline.
  Tool contract documented in [`docs/design/MCP.md`](docs/design/MCP.md). **Next:** deploy (M4).
- **2026-07-20** — **Quality round 1: PCI column separation + mapping resolution.** Two evidence-
  quality fixes, validated on the live RDS corpus. (1) PCI DSS body cleanup: `rePCIStopLine`
  truncates each requirement body at the column boundary ("Defined Approach Testing Procedures" /
  trailing "Guidance" — substring match because go-fitz concatenates column headers onto one line);
  audit: 0/351 noisy bodies (was 282/351). (2) Mapping resolution: CSF ISO 27001 references now
  emit `A.x.y` for Annex A citations (matching `iso.go`'s stored `citation_norm`; bare "Control
  N.M" = Annex A shorthand), and the `CCMv4.0` reference_source row drops its version pin so edges
  resolve via `is_current` (corpus has v4.1). Resolved edges 2056 → 2961 of 3254 (63.2% → 91.0%);
  the 293 still-unresolved target framework versions not in the corpus (CSF 1.1, CIS v8.0) — real
  gaps, correctly reported. Re-normalized + re-embedded (Kaggle T4) the 591 affected chunks.
  Hybrid eval flat within noise (open 66.0/45.1, filtered 80.0/62.5) — the golden set measures
  citation-finding, which the noise wasn't blocking; the wins are served-evidence quality. All
  floors pass.
- **2026-07-20** — **Quality round 2: curated titles (COBIT + 27002), 27001 chunk enrichment,
  golden v3.** (1) 373 new curated titles (276 COBIT practices/objectives/domains + 97 ISO 27002)
  authored by agents and adversarially verified — the verifier caught 10 wrong-topic 27002 titles
  (adjacent-number confusions) and 1 COBIT, all fixed before merge; 1101 total title rows.
  (2) Retrieval enrichment: each 27001 Annex A chunk now appends its 27002 equivalent's guidance
  under a `[equivalent iso27002 x.y]` label (index-layer only; served body unchanged) — driven by
  the 186 resolved structural edges via the new `ListEquivalentBodies` query. (3) Golden v3:
  125 cases (+8 COBIT, +5 OOS, +4 27001 topic-phrased, +3 27017/27018), adversarially verified
  (1 ambiguous case reworded). Results: every new COBIT and 27001 topic case hits at rank 1 —
  COBIT went from neutral "Practice EDM01.01" labels to topically searchable. New baselines
  (125 cases): open 69.6/47.7, filtered 82.6/67.0; old-105 filtered 80.0/62.9, open 66.0/43.1
  (MRR −2.0pp from enriched-27001 vs 27002 cross-framework competition — accepted trade).
  Floors re-based: recall ≥0.66, MRR ≥0.44, current ≥0.98, abstain ≥0.90 (10 OOS structurally
  fail while score-abstention is inert). 511 chunks re-embedded (Kaggle T4).
- **2026-07-20** — **Quality round 3: TSC + CCM curated titles (with a measured regression and
  repair), ISO 27001 Amd 1 landed, mapping-sheet mining ruled out.** (1) 617 curated titles
  authored + adversarially verified (TSC 393 — verifier caught a CC6.5 misattribution and a
  CC7.3→7.5 one-position shift; CCM 224 — clean). First deploy REGRESSED retrieval (open
  69.6→67.0, filtered 82.6→80.0): the paraphrases dropped canonical vocabulary — CCM had been
  using official workbook titles as its public title (a pre-existing licensing seam the curated
  titles now close). Repair agents re-authored term-preserving paraphrases (keep the searchable
  domain nouns, change the phrasing); result beats every prior baseline: open 72.2/49.5,
  filtered 81.7/67.9. Titles now cover all licensed frameworks: 1718 rows. (2) Amendment
  parsing landed: `BuildISOAmendmentTree` + doc-role dispatch (gated on the base main document
  existing in silver — ISO 22301 Amd 1 stays correctly deferred), `amends_citation_norm`/
  `amend_action` plumbed through writeTree; ISO 27001:2022/Amd 1:2024 normalized as 2 `add`
  rows (4.1, 4.2 climate-action edits); `findControl` now prefers main-document rows so
  amendments never shadow base clauses. Corpus: 12 documents / 3404 controls. (3) Publisher
  mapping sheets ruled out: CCM v4.1.0's "Scope Applicability (Mappings)" sheet is an empty
  placeholder and the CIS v8.1.2 workbook carries no mapping sheets — OLIR/CIS mappings need
  new acquisitions (v0.2.0). README rewritten (agent-authored, reviewed).
- **2026-07-21** — **Quality round 4: amended_by in the document tool + raw-cosine abstention.**
  (1) `document` now returns `amended_by` — amendment patches targeting the looked-up control
  (citation, action, qualifier, doc key, neutral title; verbatim instruction body full-projection
  only, stripped under reduced). (2) Score-floor abstention moved from the MCP core into the
  retriever and re-based on the **best raw cosine across hits** (RRF scores have no absolute
  scale; cosine does) — BM25-only deployments never trip it; `cmd/eval -abstain-floor` measures
  it directly. Calibrated by sweeping 0.30–0.70 on the 125-case set: floor 0.5 abstains the two
  clearly-distant OOS queries at zero recall/MRR cost (abstention 92.0% → 93.6%); above 0.55
  in-scope cases start tripping — compliance-adjacent OOS embeds too close to InfoSec text to
  separate further at this corpus size. `search_abstain_floor` seeded to 0.5.
- **2026-07-21** — **Incident: the deployed dense arm had been silently dead since first deploy;
  fixed + guarded.** Deploy-time live testing of the new abstention floor exposed that every
  production search hit was BM25-only (similarity 0, no vector rank). Root cause: the dense arm
  filters `gold.chunk_embedding` on exact model-string equality; stored rows carry
  `qwen3-embedding-0.6b` but `cmd/server`/`cmd/mcp` constructed query embedders with
  `Qwen/Qwen3-Embedding-0.6B` — zero rows matched. (The round-1 "model mismatch" embed error was
  the same defect's loud form; normalizing the response comparison silenced it without fixing the
  SQL filter.) Eval numbers were unaffected (cmd/eval used the right string), but prod served
  lexical-only retrieval. Fix: `embed.CanonicalModel`/`CanonicalDims` as the single source of
  truth across all five constructors, plus a construction-time parity guard in `retrieve.New`
  that warns loudly when zero stored embeddings match the query embedder's model. Verified live:
  startup logs "dense arm ready … embeddings=3404", in-scope hits carry real similarities and
  vector ranks, and the abstention floor fires (clearly-OOS query → `abstain: true`,
  `low_confidence`, top sim 0.26 < 0.5).
- **2026-07-21** — **NIST OLIR 800-53↔27001 crosswalk ingested: +643 publisher mapping edges,
  all resolved.** Official artifact: OLIR informative reference #155 v1.0.0 (NIST-developed,
  focal SP 800-53 r5.1.1 → ISO/IEC 27001:2022), auto-fetched from csrc.nist.gov (public
  domain; the catalog's published SHA3-256 mismatches the "UPDATED" file — authenticity is
  origin-anchored, noted in the file_rule provenance). Pipeline: new `cmd/fetch` entry,
  `file_rule` row (nist80053|r5|companion-workbook:iso27001-olir), existing XLSX extract, and a
  mapedges sub-step (`EmitOLIREdges`) that parses the bronze capture — strict citation gates
  (zero-padded focals incl. 49 enhancements; 204 clause + 439 Annex A targets) validated
  against the real file with zero rejects. This edition carries no per-row relationship type
  and NIST's submission warns against equivalency assumptions, so all edges are `related`
  under mapping_source `nist-olir`, pinned to 27001:2022. Result: 3,897 total edges, 92.5%
  resolved (nist-olir 643/643); the corpus's two largest frameworks are now directly linked
  (e.g. AC-01 → 12 resolved ISO 27001 targets, verified via the live document tool).
- **2026-07-21** — **CIS v8.1 mapping workbooks ingested: +565 typed publisher edges, all
  resolved.** Three CIS-published workbooks (same CC BY-NC-ND terms as the Controls; direct
  learn.cisecurity.org downloads wired into `cmd/fetch` as `CISMappings`): ISO/IEC 27001:2022
  (199 edges), NIST CSF 2.0 (60 — the workbook genuinely maps only 60 safeguard rows; the rest
  are tracked in CIS's own "Unmapped" sheets), NIST SP 800-53 r5 (306 — one row has a blank relationship cell, a publisher
  data gap emitted as 'related'). Unlike OLIR these carry real relationship types — 457 subset-of, 72 superset-of,
  35 equivalent under mapping_source `cis-v8.1-mappings`. Parser quirk defeated: the workbooks
  store safeguard numbers as floats, merging N.1/N.10 (4.1 ≡ 4.10 numerically), so safeguards
  resolve by title match against the ciscontrols corpus — unmatched rows are counted, never
  guessed. Targets normalized per framework (A5.9→A.5.9; CM-8(1)→CM-08(01)). Emitted counts
  validated against raw workbook row counts (200/60/309 → dedupe+skip accounted). Corpus:
  4,462 edges, 93.4% resolved. Live-verified: `document 1.1 ciscontrols` returns 7 typed
  resolved edges across all three targets.
