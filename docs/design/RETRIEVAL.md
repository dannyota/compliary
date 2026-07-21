# Retrieval design

Hybrid retrieval over the `gold` layer, ported from banhmi with
[port-fit review conclusions](#port-review-summary) driving what shipped and what was dropped.
Schema in [`SCHEMA.md`](SCHEMA.md); pipeline + stack in [`../ARCHITECTURE.md`](../ARCHITECTURE.md).

## Shape

- **Dense arm:** Qwen3-Embedding-0.6B ONNX, 1024-d cosine, exact scan (not HNSW — 3.4k rows,
  ~2 ms; re-evaluate at 10k+).
- **Sparse arm:** BM25 `sparsevec` (2^20 hashing trick), English citation-aware tokenizer
  (lowercase + alnum split; citation tokens like `AC-2(3)`, `A&A`, `I&S` kept intact).
- **Fusion:** RRF (`rrf_k=20`, `lex_weight=0.5`); top_k=8, vector_k=50, bm25_k=50, doc_cap=0.
- **Version filter:** default `is_current` only; explicit version pin via `SearchOpts`.
- **Citation routing:** 10 scheme regexes (from `config.framework.citation_scheme`); matched
  citations do a direct `citation_norm` DB lookup, returned as pinned hits (rank 0, score 1.0),
  remaining slots filled by hybrid search.
- **BM25-only degradation:** if no embeddings exist (untagged build), retriever falls back to
  BM25 arm only. The non-current version disclosure pass also runs BM25-only in this mode
  (sparse arm against non-current chunks, same badging/limits as the dense path).

## Embedding strategy

- **Bulk embed:** Kaggle T4 GPU engine, auto-selected when `KAGGLE_API_TOKEN` is set and
  >=200 chunks need embedding. Uploads chunk text to the operator's **private** Kaggle dataset
  (warned in logs). CPU bulk embedding remains the supported fallback (engine=local) but is slow; Kaggle T4 is auto-selected when a token is present and the batch is large.
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

### Golden v3 baseline (125 cases — 2026-07-21, current)

125 adversarially-verified cases (105 v2 + 20 v3: 8 COBIT, 5 OOS, 4 ISO 27001 topic-phrased,
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
| Open-corpus (no pins) | 72.2% | 50.5% | 100% | 95.2% | recall ≥66%, MRR ≥44%, current ≥98%, abstain ≥90% |
| Framework-filtered | 87.8% | 72.8% | 100% | 93.6% | — |

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
  retriever) abstains 2 of 10 OOS golden cases; the other 8 are compliance-adjacent and embed
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
