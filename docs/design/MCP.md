# MCP tool contract

compliary's product surface: five read-only MCP tools that expose InfoSec control-framework evidence
to a user-owned agent. Same contract as banhmi. The agent decides the answer; compliary returns
structured data, never prose.

## Tools

| Tool | Purpose | Input | Key output |
|------|---------|-------|------------|
| **guide** | Playbook: scope, citation forms, query tips, evidence contract | none | static structured payload |
| **corpus_status** | Live per-framework/version counts | none | frameworks[] (incl. mapping_edges outbound + inbound_edges resolved-into), totals, notes |
| **search** | Hybrid retrieval (dense + BM25, RRF-fused) | query, framework?, version_label?, include_withdrawn?, top_k?, mode?, detail? | hits[], gaps[], abstain |
| **document** | Citation lookup: control + mappings + lineage + chunks | citation, framework_code?, version_label?, include? | control, amended_by, parent, children, mappings, inbound_mappings, version_lineage, chunks, gaps |
| **quality_gaps** | Known corpus gaps and caveats | category?, limit? | unresolved_mappings, deferred_docs, manifest_gaps, body_quality_caveats, eval_floors |

## Recommended agent flow

1. `guide` -- read the evidence contract.
2. `corpus_status` -- see what is indexed, which versions are current.
3. `search` with framework filter for ~83% recall (vs ~67% unfiltered open-corpus). Use `detail=compact` for cheap discovery (citations, scores, version badges only); then read full text via `document include=["chunks"]`.
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

**Amendments:** when a control has amendment patches (rows in an `amendment` document whose
`amends_citation_norm` targets it), `document` returns them in `amended_by` — citation, action
(add/replace/delete), amendment qualifier, doc key, neutral title, and (full projection only)
the verbatim instruction text. Base lookups always win citation resolution; amendment rows never
shadow the base clause.

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

**SSRF guard on CIMD fetch:** when the server fetches a client's metadata document URL, the
resolved IPs are checked against a deny list (loopback, link-local incl. 169.254.0.0/16, private
RFC 1918, ULA, multicast, unspecified). The HTTP client is pinned to vetted IPs (prevents DNS
rebinding), redirects are refused, and the response body is capped at 64 KiB.

### Rate limiting and safety

Body cap (1 MiB), cross-origin protection, panic recovery. Stateless: no session state -- all tools
are read-only queries.

**Two-tier rate limiting:**
- **Global** per-IP limiter on all routes (`COMPLIARY_MCP_RATE_RPS` default 50,
  `COMPLIARY_MCP_RATE_BURST` default 100).
- **OAuth brute-force gate:** a tight per-IP limiter layered on top, applied only to
  `POST /oauth/authorize` and `POST /oauth/token` (the operator-secret guess path + token
  endpoint). `COMPLIARY_OAUTH_RATE_PER_MIN` default 10 attempts/min. On exceed -> 429 +
  `Retry-After`.

**Client IP behind proxy:** with `COMPLIARY_TRUST_PROXY=true`, the client IP for rate limiting is
the entry the **trusted edge appended** to `X-Forwarded-For` — position `len − COMPLIARY_TRUSTED_PROXY_HOPS`
(default hops `1`, a single CloudFront edge). The **leftmost** entry is never used: edge proxies
append rather than replace, so the leftmost is client-controllable and an attacker could rotate a
spoofed value to get a fresh rate-limit bucket per request and bypass the brute-force gate. Set
`COMPLIARY_TRUSTED_PROXY_HOPS=2` for a CloudFront→ALB chain.

### Security hardening status

Follow-ups from the security review, all implemented:

- **DCR registration cap (done):** registered clients are capped (50) with 24h idle eviction
  (last token/code activity; statically-authorized clients exempt); registration past the cap
  returns 503. CIMD metadata fetches are additionally rate-limited (1/s, burst 5).
- **Refresh-reuse family revocation (done):** each token pair belongs to a family (created on
  authorization code exchange, inherited on refresh rotation). Replaying an already-consumed
  refresh token revokes every live access and refresh token in that family and returns
  `invalid_grant`.
- **PKCE verifier length check (done):** the token endpoint rejects `code_verifier` values
  outside RFC 7636's 43-128 character bound with `invalid_grant` before hashing.

## Search: score-floor abstention

The `search_abstain_floor` config setting controls when the search tool signals low confidence.
The floor compares the **best raw vector cosine similarity across the returned hits** (not the
RRF-fused score, which is a rank artifact with no absolute scale). The check lives in the
retriever, so `cmd/eval -abstain-floor` measures it directly. BM25-only deployments (no
embedder) never abstain on the floor — there is no cosine to compare.

- **Current floor: 0.5** (calibrated 2026-07-21 by sweeping 0.30–0.70 against the 125-case
  golden set). At 0.5 the two clearly-distant OOS queries abstain and no in-scope case trips;
  above 0.55 in-scope cases start failing. Honest limit: compliance-adjacent OOS (export
  controls, medical-device software, environmental management) embeds close to InfoSec text, so
  the cosine bands overlap — 8 of 10 OOS golden cases still return hits without abstaining.
- **Abstain response:** `abstain: true` + `gaps[].kind = "low_confidence"` or `"no_evidence"`.
  Hits are still returned; the agent sees the gap notice and decides how to proceed.

### Response vocabulary (consumer contract)

- **Gap kinds:** `no_evidence`, `low_confidence`, `unknown_framework`, `version_not_found`,
  `ambiguous_citation`, `found_elsewhere` (citation absent from the pinned framework but present
  in the listed ones), `no_chunks` (chunks requested but none exist at this offset). Filter gaps
  (`unknown_framework`/`version_not_found`) fire even when hits are returned — advisory
  (`blocks_answer: false`) with hits, blocking without.
- **Hit shape:** citation, content, RRF score, version badge, ready-to-paste cite, and
  `source_url` (official publisher page from bronze provenance — also on `document`'s control).
  Retrieval internals (chunk/document IDs, per-arm scores/ranks) are not exposed.
- **Search detail levels:** `detail=standard` (default) returns the full hit shape including
  content and context_prefix. `detail=compact` strips content and context_prefix from every hit
  (keeps citation, citation_norm, framework_code, version_label, score, version badge, cite,
  source_url) — the cheap discovery pass for token economy. The agent then reads full text via
  `document include=["chunks"]`. Compact composes orthogonally with reduced projection
  (projection stripping still applies; compact is additive).
- **Input validation:** `document.citation` is required (schema + runtime); an unrecognized
  `include` section name is a hard error naming the valid set (`chunks`, `mappings`, `lineage`,
  `children`) — never a silently empty response. `quality_gaps.category` errors list the valid
  categories. `search.detail` rejects unknown values naming the valid set (`compact`, `standard`).
- **Operator-tunable** via the config.setting seed row; re-calibrate with
  `cmd/eval -abstain-floor <f>` when the corpus grows.

### Abstention eval status

| Lane | Recall@8 | MRR@8 | Current | Abstain |
|------|----------|-------|---------|---------|
| Open-corpus (no pins) | 67.0% | 47.2% | 100% | 95.2% |
| Framework-filtered | 83.5% | 67.7% | 94.3% | 93.6% |

Golden set v3: 125 cases (105 v2 + 20 new: 8 COBIT, 5 OOS, 4 ISO 27001 topic-phrased, 3
27017/27018). Quality round 2 (2026-07-20): curated titles for COBIT + 27002 put every new
COBIT topic case at rank 1; 27001 Annex A chunk enrichment (27002 guidance appended to the
retrieval chunk under a source label) put every 27001 topic case at rank 1. On the old 105-case
set the filtered lane is 80.0/62.9 (back to baseline) and open is 66.0/43.1 (MRR −2.0pp from
cross-framework competition between enriched 27001 chunks and their 27002 twins — an accepted
trade; the MCP-recommended filtered path improved).

Floors (open-corpus lane, 125-case set): recall >= 0.66, MRR >= 0.44, current >= 0.98,
abstain >= 0.90. The raw-cosine floor (0.5) catches the clearly-distant OOS cases; the
remaining compliance-adjacent OOS cases embed too close to InfoSec text to separate —
95.2% open-lane abstention accuracy is the calibrated optimum at this corpus size
(re-baselined 2026-07-21; invocation in RETRIEVAL.md).

**Truth about abstain floors:** the raw-cosine floor (0.5) separates only the clearly-distant
out-of-scope queries — 2 of the 10 OOS golden cases abstain; the other 8 are compliance-adjacent
(export controls, medical devices, environmental management) and embed too close to InfoSec text
to separate at this corpus size. `no_evidence` fires on empty result sets regardless. This is
honest: the floor catches what it can prove, and the remaining overlap is documented rather than
papered over.

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
4. **Out-of-scope query (GDPR)** -- `search` returned 3 hits, `abstain=false`, 0 gaps (expected at
   the time: the floor was 0 during this validation; the raw-cosine floor landed later).

**Verdict:** an agent can work through MCP alone. The tool contract is functional for real
compliance work. See the full transcript in `.superpowers/sdd/m3-task-3-report.md`.
