# MCP tool contract

compliary's product surface: five read-only MCP tools that expose InfoSec control-framework evidence
to a user-owned agent. Same contract as banhmi. The agent decides the answer; compliary returns
structured data, never prose.

## Tools

| Tool | Purpose | Input | Key output |
|------|---------|-------|------------|
| **guide** | Playbook: scope, citation forms, query tips, evidence contract | none | static structured payload |
| **corpus_status** | Live per-framework/version counts | none | frameworks[], totals, notes |
| **search** | Hybrid retrieval (dense + BM25, RRF-fused) | query, framework?, version_label?, include_withdrawn?, top_k?, mode? | hits[], gaps[], abstain |
| **document** | Citation lookup: control + mappings + lineage + chunks | citation, framework_code?, version_label?, include? | control, parent, children, mappings, inbound_mappings, version_lineage, chunks, gaps |
| **quality_gaps** | Known corpus gaps and caveats | category?, limit? | unresolved_mappings, deferred_docs, manifest_gaps, body_quality_caveats, eval_floors |

## Recommended agent flow

1. `guide` -- read the evidence contract.
2. `corpus_status` -- see what is indexed, which versions are current.
3. `search` with framework filter for 80% recall (vs 65% unfiltered open-corpus).
4. `document` for citation-keyed traversal: body, mapping edges (both directions), version lineage.
5. `quality_gaps` to surface what the corpus cannot answer.

## Projections

Two projection modes control whether verbatim licensed text is included in responses:

| Mode | When | Includes |
|------|------|----------|
| **Full** | stdio (local operator), authenticated HTTP | body, title_original, chunk content |
| **Reduced** | unauthenticated HTTP | citations, paraphrased titles, scores, mapping edges; body/title_original/content stripped |

Mapping edges, version lineage, and structural metadata survive both projections unchanged --
they are structural, not licensed text.

## Transports

| Transport | Entry point | Projection | Auth |
|-----------|-------------|------------|------|
| **stdio** | `cmd/mcp` | full always | none (local operator) |
| **Streamable HTTP** | `cmd/server` `/mcp` | depends on auth | OAuth or bearer token |

### HTTP auth (M4)

Three modes, selected by env vars (checked in order):

1. **OAuth** (preferred): `COMPLIARY_PUBLIC_URL` + `COMPLIARY_OAUTH_OPERATOR_SECRET` both set. Full
   MCP auth spec: RFC 9728 protected-resource metadata, RFC 8414 authorization-server metadata,
   RFC 7591 dynamic client registration, authorization-code + PKCE (S256), Client ID Metadata
   Documents (CIMD). Connects as a custom connector in **claude.ai** and **chatgpt.com**. Full
   projection for authenticated callers. If `COMPLIARY_MCP_TOKEN` is also set, static bearer
   tokens are accepted as a fallback (CLI/script backward compat).

2. **Bearer-only**: only `COMPLIARY_MCP_TOKEN` set. Existing behavior -- full projection for
   valid bearer, 401 otherwise.

3. **No auth**: neither set. Projection = reduced (no body/title_original/chunk content).
   `COMPLIARY_MCP_PUBLIC=true` serves the reduced surface anonymously; default (false) returns 401
   on all `/mcp` requests.

**OAuth endpoints** (served without bearer auth -- they ARE the auth mechanism):

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/.well-known/oauth-protected-resource` | GET | RFC 9728 protected resource metadata |
| `/.well-known/oauth-authorization-server` | GET | RFC 8414 authorization server metadata |
| `/oauth/register` | POST | RFC 7591 dynamic client registration |
| `/oauth/authorize` | GET/POST | Authorization code flow (login form + consent) |
| `/oauth/token` | POST | Token exchange (auth code + PKCE, refresh) |
| `/oauth/jwks` | GET | Empty JWKS (opaque tokens, no signing keys) |

**Token design:** opaque tokens with in-memory store. Single-user, single-process -- no JWT
overhead, no key management. Access tokens 1h, refresh tokens 7d, auth codes 10min.

**Client registration:** both DCR and CIMD supported. Claude.ai and ChatGPT prefer CIMD
(`client_id_metadata_document_supported: true`). DCR clients get a secret (confidential); CIMD
clients are public (no secret). Token endpoint supports `token_endpoint_auth_method: "none"` for
public clients and `"client_secret_post"` for DCR clients.

**PKCE:** required, S256 only.

**Redirect URIs:** port-agnostic matching for localhost/127.0.0.1 (Claude Code uses varying ports).

**`iss` in authorization response:** included per RFC 9207.

**Auth-exempt paths:** `/healthz`, `/.well-known/*`, `/oauth/*` bypass bearer auth.

### Rate limiting and safety

Body cap (1 MiB), cross-origin protection, panic recovery. Stateless: no session state -- all tools
are read-only queries.

## Search: score-floor abstention

The `search_abstain_floor` config setting controls when the search tool signals low confidence:

- **Current floor: 0.** At 3.4k chunks with RRF fusion, the score band is too compressed
  (0.047--0.071 for non-citation hits) for clean OOS/in-scope separation. Any floor > 0 drops
  in-scope recall.
- **Abstain response:** `abstain: true` + `gaps[].kind = "low_confidence"` or `"no_evidence"`.
  The agent sees the gap notice and can decide how to proceed.
- **Operator-tunable:** as the corpus grows and score distributions widen, operators raise the
  floor via the config.setting seed row.

### Abstention eval status

| Lane | Recall@8 | MRR@8 | Current | Abstain |
|------|----------|-------|---------|---------|
| Open-corpus (no pins) | 65.0% | 44.6% | 100% | 95.2% |
| Framework-filtered | 80.0% | 62.9% | 94.2% | 95.2% |

Floors (open-corpus lane): recall >= 0.63, MRR >= 0.41, current >= 0.98, abstain >= 0.93.
Abstain floor is 0 (no score-based abstention); the 95.2% accuracy comes from the 5 OOS cases
all returning hits (no `no_evidence` gap) but the in-scope cases also returning hits -- the gap
is the score-floor mechanism, not the evidence presence.

**Truth about abstain floors:** at the current corpus size, score-floor abstention cannot distinguish
OOS queries from in-scope queries. The `abstain` field works for `no_evidence` (empty result set)
but the `low_confidence` path is effectively inert (floor=0). This is honest: the machinery is
wired and operators can tune it as the corpus grows.

## ISO-family structural equivalence edges

186 bidirectional `equivalent` edges: ISO 27001:2022 A.x.y maps to ISO 27002:2022 x.y (93 pairs).
Mapping source: `iso-structural` (derived from ISO numbering structure, not a publisher mapping
table). All 186 edges resolve (both 27001 and 27002 are ingested).

27017/27018 mappings intentionally omitted: both use 27002:2013 numbering, not 27002:2022.
compliary does not ingest 27002:2013, so these mappings cannot be derived structurally with
certainty.

## Haiku stand-in validation summary

A Haiku agent drove the real stdio MCP server (BM25-only mode, no ONNX) end-to-end with no repo
access. Four tasks:

1. **PCI DSS MFA search** -- `search` with `framework=pcidss` returned Req 8.4, 8.5, 8.3.11
   (correct MFA requirements).
2. **NIST CSF PR.AA-01 mapping traversal** -- `document` returned 59 outbound mappings across 6
   frameworks (nist80053, iso27001, ciscontrols, pcidss, csaccm, nistcsf) + 1 inbound mapping.
3. **ISO 27001 A.5.1 currency + 27002 equivalent** -- `document` returned version_status=current,
   1 outbound iso-structural equivalent edge to iso27002 5.1 (resolved=true), 1 inbound mapping.
4. **Out-of-scope query (GDPR)** -- `search` returned 3 hits, `abstain=false`, 0 gaps (expected:
   floor=0 means no score-based abstention; the InfoSec hits are low-relevance tangential matches).

**Verdict:** an agent can work through MCP alone. The tool contract is functional for real
compliance work. See the full transcript in `.superpowers/sdd/m3-task-3-report.md`.
