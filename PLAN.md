# compliary plan

Status: Archived. Development and maintenance have stopped. Hosted endpoints are unsupported.
The roadmap, deployment descriptions, and unfinished work below are historical references.
No further development is planned. See [archive status](README.md).

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
assessed against. Unlike regulations, these are **few, stable documents** (~17 frameworks, ~27
files). **v0.1.0 built on the 12 frameworks acquired; v0.2.0 adds TCVN 11930:2017 + TCVN
14423:2025; the 5 deferred phase-2 documents follow across 0.2.x.**

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
| 16 | TCVN 11930 | 2017 | Bộ KH&CN (Quyết định 2582/QĐ-BKHCN; biên soạn: Cục ATTT) | paid, copyrighted (VSQI) | BYO (purchase) | Clause requirement per security level 1–5 |
| 17 | TCVN 14423 | 2025 | Bộ KH&CN (biên soạn: Cục An ninh mạng — A05, Bộ Công an) | paid, copyrighted (VSQI) | BYO (purchase) | Clause requirement |

- **SOC 2 Type 1 vs Type 2** are *report types* (design vs operating effectiveness over a period),
  not separate criteria sets — one corpus entry over the TSC, report-type as metadata.
- **ISO cloud companion 27017 lags the 2022 renumbering:** still keyed to 27002:2013 control
  numbering (27018:2025 realigned to 27002:2022) — version-lineage relations must model
  cross-edition references.
- **TCVN standards are Vietnamese-language** (publication language) with first-class mapping-edge
  candidates (both verified Active on VSQI 2026-07-24): **TCVN 11930:2017** is MOD-equivalent to
  ISO/IEC 27001:2013 + NIST SP 800-53 r4; **TCVN 14423:2025** (Quyết định 1019/QĐ-BKHCN,
  2025-05-29) is MOD-equivalent to CIS Controls v8 and normatively references TCVN 11930:2017 —
  it complements, does not supersede.
- **Parser build order:** see [`docs/design/SCHEMA.md`](docs/design/SCHEMA.md) (OSCAL → XLSX → PDF).

**License gates (verified against live publisher sources 2026-07-19):**

- **NIST** — public domain (17 U.S.C. §105); auto-fetch and verbatim serving unrestricted.
- **CIS Controls** — CC BY-NC-ND 4.0 (Benchmarks: CC BY-NC-SA 4.0); registration-gated download.
- **PCI DSS** — click-through license: internal use + employee study only; no sublicense, no
  modification, no derivative works.
- **ISO** — all copying/distribution prohibited without written permission.
- **AICPA TSC** — personal non-commercial use only; explicit objection to LLM/AI knowledge-base
  inclusion — most conservative source.
- **TCVN (VSQI)** — verified 2026-07-24: publication copyright held by Viện Tiêu chuẩn Chất lượng
  Việt Nam (tieuchuan.vsqi.gov.vn); paid purchase (price on contact / member packages), accounts
  non-transferable. No public download — BYO purchase, `auth-text-only` serving; never ingest the
  third-party re-hosts (thuvienphapluat, scribd, …).

**Acquisition status (2026-07-24): 12 of 16 frameworks have documents in `data/`.**
Remaining acquisitions land across the 0.2.x line — TCVN 11930:2017 first (v0.2.0):

- **TCVN 11930:2017** — in hand (`data/tcvn/tcvn-11930-2017.pdf`) — ready to ingest.
- **TCVN 14423:2025** — in hand (`data/tcvn/tcvn-14423-2025.pdf`).
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

### v0.1.0 — corpus + serve (12 acquired frameworks) — DONE

All milestones shipped and live (details: [`docs/HISTORY.md`](docs/HISTORY.md)):

- **M0-M2** (done): bootstrap, `cmd/fetch`, schema layer, OSCAL/XLSX/PDF extract, all 8 parsers
  (11 docs / 3,402 controls), hybrid retriever + eval harness.
- **M3** (done): five MCP tools over the query core; stdio + Streamable HTTP transports;
  contract in [`docs/design/MCP.md`](docs/design/MCP.md).
- **M4** (done, live): `compliary.danny.vn` — OAuth `/mcp`, co-located on banhmi's ECS host,
  CloudFront ingress; topology in [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

### v0.1.x — patch releases (complete)

Improvements to the shipped v0.1.0 without new frameworks: parser fixes, retrieval-quality tuning,
eval-floor raises, doc/metadata corrections, dependency bumps. Corpus additions never land in a
patch — new documents always cut v0.2.0+.

Shipped: **0.1.11** (review round: MCP consumer contract, OAuth hardening, fetch/pipeline/parser
fixes), **0.1.12** (OAuth token families + PKCE bounds; `release.sh`; CI), **0.1.13** (parser
guard hardening; landing single-source; write deadlines), **0.1.14** (quality round 7: mapping
resolution 1,198→233 unresolved; search `detail` levels; eval re-baseline ~83%/~67%; BM25-only
lineage; HNSW drop; honest User-Agent), **0.1.15** (landing GEO/SEO: JSON-LD, robots,
sitemap), **0.1.16** (diff review: atomic refresh rotation; release guards; Dependabot) — see
[`docs/HISTORY.md`](docs/HISTORY.md).

The v0.1.x operator follow-ups were completed before v0.2.1: ECR scan-on-push,
the health alarm, and the production corpus/migration backlog are live.

### v0.2.0 — TCVN 11930:2017 + TCVN 14423:2025

The first Vietnamese-language frameworks — decided 2026-07-24; the deferred phase-2 documents move
to 0.2.x follow-ups.

**Ingestion done (round closed 2026-07-24):**

- **Extract + normalize:** `txt` extractor (bronze); `tcvn-clause` parsers with shared engine —
  **11930:** 225 controls + 65 clauses (levels 5–9 + Annex A; structural leaf detection);
  **14423:** 182 controls + 67 clauses (section 4 = **15** families, section 5 = 18 mirroring
  CIS v8). Vietnamese verbatim throughout.
- **Titles:** 407 Vietnamese paraphrased `control_title` rows (our own wording) on the reduced
  surface; paraphrase-distance machine-checked.
- **Index:** 539 TCVN chunks, dense + BM25 sparse; tokenizer verified Vietnamese-correct
  (diacritics preserved, syllable split). Vietnamese query-side stopwords landed 2026-07-25
  (19 function syllables; −14.1% query dims on the 10 TCVN cases, BM25-arm aggregates unchanged).
  Deferred tuning: per-batch IDF.
- **Eval:** 30 Vietnamese golden cases (DB-verified targets), part of the 205-case golden v6
  baseline recorded 2026-07-25 — open 86.3/66.3/100/96.6, filtered 93.2/81.3/100/95.6, floors
  unchanged (66/44/98/90). All 30 hit rank 1–3 in both lanes; the level-qualified 11930 titles
  are what make `cấp độ N` queries resolve (see RETRIEVAL.md).
- **Mappings:** neither document ships a clause-level crosswalk table, so edges are
  **operator-curated** (`config.curated_mapping` → `mapping_source='operator'`, seeded from
  `deploy/seed/curated_mapping.csv`; consumed by the mapedges stage): 39 edges, all resolved —
  14423 §5 families → CIS v8 Controls (18; 5.1 risk has no CIS peer, omitted), 14423 → 11930
  explicit normative references (4), 11930 Annex A physical (Level 3) → 800-53 r5 PE + ISO
  27001:2022 A.7 (17). Each row is our own analysis of both endpoints' text with an honest note on
  MOD-lineage version drift (11930 ← ISO 27001:2013 + 800-53 r4; 14423 ← CIS v8; corpus holds the
  later editions). Relation is `related` — the vocabulary has no `derived-from`.

**Cut prep done (2026-07-25):** index engine guard (Kaggle explicit opt-in only); golden v5
baseline re-recorded over 185 cases — open 84.7/63.1/100/96.2, filtered 92.4/79.6, floors
unchanged (66/44/98/90), pre-TCVN control run reproduced v4 exactly.

**Released 2026-07-25** (`0.2.0-20260725` live): prod corpus synced (17 frameworks / 3,943
controls / 3,943 chunks dense+sparse / 4,462 mapping edges — the round-7 prod backlog cleared
with it), image rolled, live version + auth verified.

**Released 2026-07-25** (`0.2.1-20260725` live, task-def rev 23): the TCVN improvement round —
per-framework language guidance, 39 curated mapping edges (4,501 total), level-qualified 11930
titles + re-embedded chunks, Vietnamese BM25 stopwords, golden v6, and the mapedges staleness
marker. Prod corpus re-synced by dump/restore (17 frameworks / 3,943 controls / 3,943 chunks
dense+sparse / 4,501 edges incl. 39 operator / 2,125 curated titles / 0 stale documents — the
marker carries across a restore, so no post-migration mapedges run was needed); live version,
`/healthz` 200 and unauthenticated `/mcp` 401 all verified.

### v0.2.2 — durable OAuth sessions

**Released 2026-07-30** (`0.2.2-20260730` live, task-def rev 24; image
`sha256:af4946964527a12adbf99c7c6282d581835d269b97189cb11556b30a2bd7d894`). OAuth identities,
clients, authorization codes, token families, and opaque access/refresh token hashes now live in
PostgreSQL instead of process memory. Sessions use a sliding 30-day inactivity window, survive
restarts and multiple instances, reapply authorization on login/use/refresh, and retain
refresh-replay family revocation. The default policy permits every successful password login;
verified normalized email fields and an `Authorizer` seam are reserved for a later email policy.

The AWS release path now builds `/migrate` into the server image, runs it as a checked no-port ECS
task before service rollout, clones the service's exact active task definition, enables
circuit-breaker rollback, and promotes `latest` only after public smoke checks. Container build
contexts exclude local credentials and licensed corpus data. CI applies all migrations to
PostgreSQL and runs the OAuth persistence/concurrency suite (including the race detector).
Production migration revision 1 exited successfully; `/healthz`, version, OAuth discovery, and
unauthenticated `/mcp` checks passed. A fresh Codex process then reused the configured OAuth
connection and called `corpus_status` successfully (14 documents, 13 framework versions, 3,943
controls/chunks).

### Settled — mapedges staleness marker (2026-07-25)

**Mapedges staleness was invisible.** Normalize cascade-deletes a framework's outbound mapping
edges (`ON DELETE CASCADE`) and `mapedges` is not in the default stage list, so a scoped
re-normalize silently dropped them (this bit the TCVN round: 17 curated edges vanished, caught
only in review). Curated-row drift covered 39 of 4,501 edges; derived edges — e.g. nistcsf's
2,868 — had no detection.

**Landed** (mirrors the index stage's `indexed_at`, per both reviewers; mapedges stays **out** of
the default stage list):

- **`silver.document.mapedges_at`** — per-document marker, the exact grain the cascade destroys.
- **NULLed by normalize** inside `DeleteControlsForDocument`'s data-modifying CTE (same statement
  as the delete — cannot be forgotten), plus a **WARN** carrying `edges_deleted` /
  `edges_reemitted_by_parser` / `needs_mapedges`.
- **Stamped corpus-wide** by `normalize.MarkMapEdgesFresh` only after the whole mapedges stage
  succeeds; the stage now also logs per-source emission counts.
- **Surfaced** as `quality_gaps category=stale_mapedges`, distinguishing a never-run corpus from a
  re-normalize regression.

Validated on the dev corpus: re-normalizing tcvn14423 dropped 4,501 → 4,479 edges (operator
39 → 17), the WARN fired with `edges_deleted=22`, `stale_mapedges` named the document; `-stage
mapedges` restored 4,501 / 39 with 0 stale documents.

### v0.2.x — phase-2 corpus completion (follow-ups)

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
| Corpus language | publication language per framework — English for international frameworks, Vietnamese for TCVN (from v0.2.0); never translates. Search guidance: query in the framework's publication language |
| Stack | Go + PostgreSQL/pgvector + sqlc + hybrid retrieval + MCP (banhmi-proven) |
| Sources | official publisher sources only; license provenance per document |
| Maintainer `/mcp` auth | **OAuth** (MCP auth spec: authorization-code + PKCE + dynamic client registration) so the instance connects as a **claude.ai and chatgpt.com custom connector**; **unauthenticated requests get 401 for everything** (no anonymous reduced surface on the maintainer instance — projection layer stays as defense-in-depth; self-deployers may opt into a public metadata surface) |
| Embedder | maintainer deploy **shares banhmi's embedder** (wiring at M4); self-deploy ships embed/lexindex/retrieve code **copied from banhmi** (same author) at the Index stage |

## Milestone history

Moved to [`docs/HISTORY.md`](docs/HISTORY.md) — the dated record of every milestone and quality
round. This plan stays forward-looking.
