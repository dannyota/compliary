# Retrieval design

Hybrid retrieval over the `gold` layer, ported from banhmi with
[port-fit review conclusions](#port-review-summary) driving what shipped and what was dropped.
Schema in [`SCHEMA.md`](SCHEMA.md); pipeline + stack in [`../ARCHITECTURE.md`](../ARCHITECTURE.md).

## Shape

- **Dense arm:** Qwen3-Embedding-0.6B ONNX, 1024-d cosine, exact scan (not HNSW — 3.4k rows,
  ~2 ms; re-evaluate at 10k+).
- **Sparse arm:** BM25 `sparsevec` (2^20 hashing trick), language-neutral citation-aware tokenizer
  (lowercase + alnum split, diacritics preserved so each Vietnamese syllable is a token; citation
  tokens like `AC-2(3)`, `A&A`, `I&S` kept intact).
- **Query-side stopwords:** function words are dropped from the QUERY vector only (documents keep
  every term — no re-index). English list + a Vietnamese list for the TCVN corpora (conjunctions,
  determiners, prepositions, copula/passive marker, interrogatives). Function words only: syllables
  that head or complete a domain compound stay (`theo` → theo dõi, `không` → không dây, `từ` → từ
  xa, `sao` → sao lưu, `vào`/`ra` → ra vào). `an` (an toàn) is filtered as the English article —
  a deliberate cross-language collision, since the majority-English corpus makes it the least
  discriminative token and `toàn`/`ninh` carry the phrase.
- **Fusion:** RRF (`rrf_k=20`, `lex_weight=0.5`); top_k=8, vector_k=50, bm25_k=50, doc_cap=0.
- **Version filter:** default `is_current` only; explicit version pin via `SearchOpts`.
- **Citation routing:** 10 scheme regexes (from `config.framework.citation_scheme`); matched
  citations do a direct `citation_norm` DB lookup, returned as pinned hits (rank 0, score 1.0),
  remaining slots filled by hybrid search.
- **BM25-only degradation:** if no embeddings exist (untagged build), retriever falls back to
  BM25 arm only. The non-current version disclosure pass also runs BM25-only in this mode
  (sparse arm against non-current chunks, same badging/limits as the dense path).

## Embedding strategy

- **Bulk embed:** local ONNX by default. The Kaggle T4 GPU engine is **explicit opt-in only**
  (`COMPLIARY_EMBED_ENGINE=kaggle`) — it uploads chunk text to the operator's **private** Kaggle
  dataset (warned in logs), so a configured `KAGGLE_API_TOKEN` alone never selects it. CPU bulk
  embedding is slow for large batches; opt into Kaggle deliberately for those runs.
- **Query-time embed:** two paths, selected at runtime. `COMPLIARY_EMBED_ENDPOINT` set → an
  OpenAI-compatible HTTP embedder (the deployed path: banhmi's shared embedder; no ONNX in the
  image). Unset → local ONNX in-process (requires `-tags onnx` build + model at
  `~/.cache/banhmi/qwen3-embedding`). The Qwen3 instruction prefix (`embed.FormatQuery`) is applied
  in the retrieve layer, so it is identical on both paths.

## Port review summary

Review evaluated banhmi `pkg/rag/{embed,lexical,retrieve}` + `cmd/{lexindex,eval}` for compliary.

| Verdict | Count | Examples |
|---------|-------|---------|
| **Copy** | 8 | ONNX embedder, BM25 core, RRF fusion, evidence envelope |
| **Adapt** | 14 | Query prefix, Hit struct, SearchOpts, hybridRetriever, index scoping |
| **Drop** | 9 | VN diacritics, Thai segmenter, identifier scope, rollup, abbreviation expand |
| **Build fresh** | 2 | Citation regex registry, direct-lookup retriever |
| **Defer** | 3 | Rollup/section-aggregate, reranker |

**Dropped VN machinery:** `NormVNFold`, `DiacriticFree`, `restoreDiacritics`, `NormTH`/TCC,
`lexicalWeightFor` (VN diacritic routing), `expandIndonesianRef`, `extractDocumentRefs`
(so-ky-hieu patterns), `identifierScope`, `WithDiacriticDict`, `attachArticles`. These are
language- or jurisdiction-specific; compliary is English-only with citation-keyed controls.

**Dropped scale machinery:** HNSW index dropped from schema (exact scan wins at 3.4k;
re-evaluate at 10k+), rollup/section-aggregate (1 chunk/control), SageMaker bulk embed,
doc_cap (12 documents total).

## Baselines

### Golden v6 baseline (205 cases — 2026-07-25, current)

205 cases = the 185-case v5 set + 20 Vietnamese TCVN cases, taking the TCVN block to 30.
The 20 new cases: 8 per-level 11930 body queries ("cấp độ N …"), 5 Annex A physical-security
level queries, 5 TCVN 14423 §4 (state-agency systems) vs §5 (national-security systems)
disambiguation queries, 2 mapping-flavoured queries. All targets DB-verified before pinning;
all queries author-phrased Vietnamese, not title echoes. The `tcvn14423-dmarc` query was
rephrased away from its control title (id and target unchanged).

| Lane | Recall@8 | MRR@8 | Current | Abstain | Floor |
|------|----------|-------|---------|---------|-------|
| Open-corpus (no pins) | 86.3% | 66.3% | 100% | 96.6% | recall ≥66%, MRR ≥44%, current ≥98%, abstain ≥90% |
| Framework-filtered | 93.2% | 81.3% | 100% | 95.6% | — |

Floors carried over from v4/v5 unchanged (all comfortably met); numbers and floors also live in
[`MCP.md`](MCP.md) (single source: MCP.md wins on conflict). Same reproducible invocation as v4
below; open-corpus lane adds `-use-pins=false`.

**All 30 TCVN cases hit rank 1–3 in both lanes** (23 of 30 at rank 1 in the filtered lane).
The set-size change means the v5 and v6 aggregates are not directly comparable; a control run
of the 185-case v5 set against the same post-round corpus reproduced v5 exactly
(84.7/63.2/100/96.2 open, 92.4/79.7/100/95.1 filtered), so the v5→v6 aggregate lift is the new
Vietnamese cases scoring above the corpus average, not a retriever change.

**Level-qualified titles are load-bearing.** 225 of the 290 tcvn11930 chunks carry a
`cấp độ N` qualifier that exists only in the paraphrased title (only 5 bodies mention a level
natively). Re-running the 13 level-graded cases with the level phrase stripped from the query
drops MRR@8 from 96.2% to 59.0% (open) and from 100% to 61.5% (filtered) — recall@8 stays 100%
in both, so the qualifier decides *which* level ranks first, not whether the family is found.
Six of the 13 targets have a body byte-identical to a sibling level, and every one of those
still lands at rank 1 in both lanes with the level phrase present.

### Golden v5 baseline (185 cases — 2026-07-25, superseded)

185 cases = the 175-case v4 set + 10 Vietnamese TCVN cases (5 tcvn11930, 5 tcvn14423; all
DB-verified targets; all hit at rank 1–3 in both lanes). First baseline covering Vietnamese
content (539 TCVN chunks, dense + BM25).

| Lane | Recall@8 | MRR@8 | Current | Abstain | Floor |
|------|----------|-------|---------|---------|-------|
| Open-corpus (no pins) | 84.7% | 63.1% | 100% | 96.2% | recall ≥66%, MRR ≥44%, current ≥98%, abstain ≥90% |
| Framework-filtered | 92.4% | 79.6% | 100% | 95.1% | — |

The pre-TCVN 175-case control run on the same corpus reproduced v4 exactly
(83.8/62.5/100/96.0), confirming the English lanes were unaffected by the TCVN round.

### Golden v4 baseline (175 cases — 2026-07-21, superseded)

175 adversarially-verified cases (105 v2 + 20 v3 + 50 v4: 8 COBIT, 5 OOS, 4 ISO 27001 topic-phrased,
3 ISO 27017/27018). Hybrid ONNX, raw-cosine abstention floor 0.5. Reproducible invocation
(the embedder MUST initialize — a missing `COMPLIARY_ONNX_LIB` silently degrades to BM25-only):
`COMPLIARY_ONNX_LIB=$HOME/.local/lib/libonnxruntime.so CGO_LDFLAGS=-L$HOME/.local/lib
COMPLIARY_DATABASE_PASSWORD=… go run -tags onnx ./cmd/eval -abstain-floor 0.5`.
Re-baselined 2026-07-21 after 6 golden-label corrections (collision-51-iso27002 5.1->5.7,
iso27002-824 8.24->8.23, csf-idam07 ID.AM-07->ID.RA-01, pcidss-1234 12.3.4->9.1,
iso27017-121 12.1.3->6.1.1, v3-cobit-vendor-risk APO10.03->APO10.04) and the
CurrentPrecision metric refinement (version-pinned cases treat hits from the pinned version
as version-correct, fixing filtered-lane current from 94.3% to 100%). Two lanes:

| Lane | Recall@8 | MRR@8 | Current | Abstain | Floor |
|------|----------|-------|---------|---------|-------|
| Open-corpus (no pins) | 83.8% | 62.4% | 100% | 95.4% | recall ≥66%, MRR ≥44%, current ≥98%, abstain ≥90% |
| Framework-filtered | 91.9% | 80.1% | 100% | 94.9% | — |

The withdrawn-control cases (`SC-19`, `ID.GV`) pass in the filtered lane via the
`include_withdrawn` flag. Current numbers and floors also live in
[`MCP.md`](MCP.md) (single source: MCP.md wins on conflict).

### Golden v2 baseline (105 cases — 2026-07-20, superseded)

105 adversarially-verified cases (63 v2 + 42 v1 survivors), 11 frameworks, 10+ citation
schemes. Numbers are **not comparable** to v1 or v3 (different set compositions).

| Metric | Value | Floor |
|--------|-------|-------|
| recall@8 | 63.3% | 61% |
| MRR@8 | 43.2% | 41% |
| current-version | 100% | 98% |
| abstention | 95.1% | 93% |

Per-framework recall@8:

| Framework | Cases | Recall | MRR@8 |
|-----------|-------|--------|-------|
| nist80053 | 17 | 71% | 49% |
| nistcsf | 13 | 69% | 50% |
| ciscontrols | 11 | 82% | 70% |
| csaccm | 10 | 90% | 60% |
| pcidss | 9 | 44% | 20% |
| iso27001 | 7 | 43% | 33% |
| iso27002 | 7 | 43% | 32% |
| iso27017 | 6 | 67% | 38% |
| iso27018 | 6 | 0% | - |
| soc2tsc | 8 | 75% | 35% |
| cobit | 4 | 75% | 56% |

### Prior baseline (50 cases — 2026-07-20)

Superseded by golden v2. Kept for reference; not comparable (different set size/composition).

| Metric | Value | Floor |
|--------|-------|-------|
| recall@8 | 57.8% | 55% |
| MRR@8 | 34.1% | 32% |
| current-version | 100% | 98% |
| abstention | 90% | 88% |

## Known gaps

- **Abstention separates only clearly-distant OOS** — the raw-cosine floor (0.5, in the
  retriever) abstains 8 of 15 OOS golden cases; the other 7 are compliance-adjacent and embed
  too close to InfoSec text at this corpus size.
- **Withdrawn controls need the flag** — the default `status='active'` filter excludes all 273
  withdrawn controls; the `include_withdrawn` search flag reaches them (implemented; the
  SC-19/ID.GV golden cases pass with it).
- **ISO 27018 recall 0%** — all 6 cases target short annex controls (A.x.x) with minimal
  textual signal; dense and sparse arms both miss. Same root cause as short-chunk weakness.
- **Short-chunk framework recall weak** — ISO/SOC2/PCI one-liner controls lack signal for both
  dense and sparse arms. Ancestor-title enrichment attempted but net-negative. PCI column separation landed
  (0/351 noisy bodies), curated titles cover all licensed frameworks (1718), and 27001 Annex A
  chunks are enriched with their 27002 equivalents' guidance — the remaining weakness is
  genuinely short controls with little text anywhere.
- **Bare-numeric citation ambiguity** — `5.1` matches 4+ schemes; resolved only by ranking
  when no framework filter is set.
- **TCVN 14423 §4/§5 scope is not in the chunk text** — §4 (state-agency systems) and §5
  (national-security systems) restate the same control families, and the section heading that
  names the scope is not carried into the chunk (the ancestor chain is citation-only). The
  §4/§5 twins separate today only where their bodies differ; where they are byte-identical the
  ranking is a coin flip. All 5 golden §4/§5 cases land at rank 1–2, but a prior BM25 probe saw
  `5.9.2.2` lose to its §4 twin `4.9.2.2`. A scope qualifier on 14423 titles (the fix applied to
  11930 levels) would close it.
