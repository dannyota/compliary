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

## Scope — frameworks (single scope, no phasing)

The frameworks the Vietnamese market (banks, fintech, SaaS, BPO/ITO) is actually certified or
assessed against. Unlike regulations, these are **few, stable documents** (~15 frameworks, ~20
files) — one scope, built together. All **planned** — nothing ingested yet.

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
- **Build order (recommendation): NIST first** (auto-fetch + structured OSCAL = richest spike),
  then CIS + PCI DSS + SOC 2 (free BYO), then the ISO family (purchased BYO) + mappings.

**License gates (verified against live publisher sources 2026-07-19):**

- **NIST** — public domain (17 U.S.C. §105); auto-fetch and verbatim serving unrestricted.
- **CIS Controls** — CC BY-NC-ND 4.0 (Benchmarks: CC BY-NC-SA 4.0); registration-gated download.
- **PCI DSS** — click-through license: internal use + employee study only; no sublicense, no
  modification, no derivative works.
- **ISO** — all copying/distribution prohibited without written permission.
- **AICPA TSC** — personal non-commercial use only; explicit objection to LLM/AI knowledge-base
  inclusion — most conservative source.

**Acquisition status (2026-07-19): 12 of 15 frameworks have documents in `data/`.**
Remaining acquisitions are **phase 2** — M2 starts on what's in hand; add these as they arrive:

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

## Design questions (settle before code)

1. **Schema for versions + mappings** — supersession relations (`27001:2013 → :2022`) and
   cross-framework control mappings as first-class relation types with provenance.
2. **Registry shape** — framework descriptor: sources, source access (`auto-fetch`/`BYO`), parser,
   citation scheme, version lineage (analog of banhmi's jurisdiction descriptor).
3. **Reuse strategy** — new repo, port banhmi patterns (fetch client, extraction cascade, chunking,
   eval harness); no code dependency on banhmi. *Recommended; confirm.*
4. **Maintainer `/mcp` auth mechanism** — bearer token + CloudFront origin header (banhmi pattern)
   vs OAuth (needed for claude.ai custom connectors). Decide at M5.

## Roadmap

### v0.1.0 — fetch, corpus, serve (single scope)

1. **M0 — repo bootstrap** — CLAUDE.md, PLAN.md, git + signing. **DONE 2026-07-19.**
2. **M1 — `cmd/fetch` (one-shot downloader)** — **DONE 2026-07-19** (validated live: 4 NIST files,
   PCI DSS v4.0.1 via accepted license, CIS v8.1.2 guide PDF + 2 Excel workbooks from the public
   download page). Spec: prompt operator info once (name, title, company,
   country, email), cache in **gitignored `.env`** (real identity — never commit); one run downloads
   everything automatable into `data/`: **NIST + CIS** direct (CIS serves the Controls from a
   public page — no form needed); **PCI SSC** by filling the license click-through with the
   operator's identity — the operator is the accepting party. **Any
   source requiring sign-in, an account, purchase, or membership is always manual drop-in** (AICPA
   TSC, ISO family, SWIFT CSCF, COBIT; CSA if its form needs an account) — print the official URL +
   `data/` target path. Idempotent — re-run
   anytime in dev, `.env` reused. Industry/sector dropdowns: single `.env` preference
   (`financial` | `technology` | `other`) matched to the closest option, fall back to Other — no
   per-publisher taxonomy.
3. **M2 — design + parse** — `docs/ARCHITECTURE.md` + `docs/design/SCHEMA.md`; NIST OSCAL first,
   then per-framework PDF parsers; bronze → silver → gold; real rows measured.
4. **M3 — MCP evidence service** — `guide`, `corpus_status`, `quality_gaps`, `search`, `document`;
   golden set + eval gate with baseline floors.
5. **M4 — deploy maintainer instance** — `compliary.danny.vn`: public landing, **authenticated
   `/mcp`** (auth mechanism per design question 4). Reuse banhmi's AWS shape (CloudFront → ECS → RDS).

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

## Milestone history

- **2026-07-19 (later)** — Corpus acquisition: 12/15 frameworks landed in `data/` (NIST, PCI DSS,
  CIS auto-fetched; AICPA TSC, CSA CCM v4.1, ISO 27001+Amd/27002/27017/27018:2019, 22301 Amd 1,
  COBIT 2019 dropped in). Filenames normalized to kebab-case (fetchers aligned, validated live).
  Version corrections verified: 27018 → :2025, 22301 + Amd 1:2024, CCM → v4.1. Remaining 5
  acquisitions deferred to phase 2.
- **2026-07-19** — Project bootstrapped: canonical guide, plan, git repo with signed commits.
  License gates verified against live publisher sources (PCI SSC click-through, CIS CC licenses,
  ISO copyright, AICPA T&C incl. anti-LLM clause, NIST §105). Distribution model settled:
  open-source self-deploy, BYO ingestion for license-gated sources, authenticated private
  maintainer instance. Plan simplified to a single scope (15 frameworks, no phasing) with the
  one-shot `cmd/fetch` downloader as the first build step.
