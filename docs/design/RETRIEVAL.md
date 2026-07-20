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
  BM25 arm only.

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

**Dropped scale machinery:** HNSW (exact scan wins at 3.4k), rollup/section-aggregate
(1 chunk/control), SageMaker bulk embed, doc_cap (11 documents total).

## Baselines

### Golden v2 baseline (105 cases — 2026-07-20)

105 adversarially-verified cases (63 v2 + 42 v1 survivors), 11 frameworks, 10+ citation
schemes. 2 withdrawn-control cases (`SC-19`, `ID.GV`) marked `expect_fail` — retriever
`status='active'` filter excludes them; they record honest unreachability, not a retrieval
bug. Numbers are **not comparable** to the 50-case v1 baseline (different set composition).

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

- **No score-floor abstention** — 5/5 OOS queries return hits instead of abstaining
  (no score threshold; M3 follow-up).
- **Withdrawn controls unreachable** — `status='active'` filter on both retrieval arms and
  citation lookup excludes all 273 withdrawn controls. Version-pin queries about withdrawn
  controls (SC-19, ID.GV) fail; marked `expect_fail` in golden set. Future: optional
  `include_withdrawn` search flag.
- **ISO 27018 recall 0%** — all 6 cases target short annex controls (A.x.x) with minimal
  textual signal; dense and sparse arms both miss. Same root cause as short-chunk weakness.
- **Short-chunk framework recall weak** — ISO/SOC2/PCI one-liner controls lack signal for both
  dense and sparse arms. Ancestor-title enrichment attempted but net-negative; column-separation
  (PCI body noise) and structured-title expansion remain as potential next steps.
- **Bare-numeric citation ambiguity** — `5.1` matches 4+ schemes; resolved only by ranking
  when no framework filter is set.
