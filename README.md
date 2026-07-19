<div align="center">

# compliary

**Evidence-only corpus + MCP server for the Information Security & Cybersecurity control frameworks
organizations are audited against — exact control citations, version lineage, cross-framework
mappings, provenance, and explicit gaps. compliance + library.**

[Plan](PLAN.md) · [License](LICENSE)

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![MCP](https://img.shields.io/badge/MCP-Streamable_HTTP-6E40C9)](https://modelcontextprotocol.io)
[![Status](https://img.shields.io/badge/status-bootstrap-orange.svg)](PLAN.md)

</div>

---

compliary ingests each framework from its **official publisher source** and normalizes it into a
citable knowledge base — exact control citations (`A.5.1`, `AC-2(3)`, `CC6.1`, `Req 8.3.6`,
`PR.AA-01`), version lineage, cross-framework control mappings, license provenance, and coverage
gaps — served as **evidence over MCP**. It does not answer questions: your agent connects,
retrieves the evidence, and decides the answer itself.

Sibling of [banhmi](https://banhmi.danny.vn) — banhmi serves **binding law per jurisdiction**;
compliary serves **voluntary/contractual frameworks & standards** in one corpus, where framework is
a registry dimension rather than a deployment.

## Frameworks

One corpus, ~15 frameworks, scoped to what the Vietnamese market (banks, fintech, SaaS, BPO/ITO) is
certified or assessed against — published in English, applicable anywhere. Full table with versions
and verified license gates in [PLAN.md](PLAN.md).

| Source access | Ingestion | Frameworks |
|---|---|---|
| **Public / direct** | auto-fetch | NIST CSF 2.0 · NIST SP 800-53 r5 (OSCAL) · CIS Controls v8.1 |
| **Free, form-gated** | `cmd/fetch` fills the click-through with the operator's identity | PCI DSS v4.0.1 |
| **Sign-in / purchase / membership** | manual drop-in into `data/` | SOC 2 (AICPA TSC) · ISO/IEC 27001 · 27002 · 27017 · 27018 · 27701 · ISO 22301 · ISO/IEC 42001 · SWIFT CSCF · COBIT 2019 · CSA CCM v4 |

## Licensing model

Most sources are copyrighted publications; compliary never redistributes them.

- **The repo ships code + metadata only** — never licensed document text.
- **Each operator builds their own corpus** locally, under licenses they accepted themselves.
- **Licensed text is served privately only** ("internal use") — there is **no public MCP service**;
  every operator self-deploys their own instance. The maintainer's `compliary.danny.vn` hosts a
  public landing page (project info) and an `/mcp` endpoint **authenticated for the maintainer
  alone** — it serves no other users.
- **Official publisher sources only**, with license kind, source URL, and retrieval date recorded
  per document.

## Architecture (target)

Mirrors the banhmi-proven stack — ported patterns, no code dependency:

- **Go** + **PostgreSQL + pgvector** (one datastore) + **sqlc**; medallion pipeline
  (bronze → silver → gold).
- **Framework registry** — a descriptor per framework selects sources, access mode, parser,
  citation scheme, and version lineage.
- **Versions & mappings first-class** — supersession relations (`27001:2013 → :2022`,
  `CSF 1.1 → 2.0`) so superseded text is never presented as current; cross-framework mappings
  (e.g. NIST OLIR) carry provenance.
- **Hybrid retrieval** — dense embeddings + BM25 sparse vectors (`sparsevec`), RRF-fused.
- **MCP surface** — `guide` · `corpus_status` · `quality_gaps` · `search` · `document`, over stdio
  and Streamable HTTP.

## Status

**M1 done** — `cmd/fetch` downloads every automatable source (validated live: NIST, PCI DSS
v4.0.1, CIS v8.1.2). Next: design docs + NIST OSCAL parser (M2). Roadmap, design questions, and
milestones in [PLAN.md](PLAN.md).

## License

[Apache 2.0](LICENSE) — covers this repository's code and metadata only. Framework documents
ingested at deploy time remain under their publishers' licenses.
