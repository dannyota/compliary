# Retrieval design

Hybrid retrieval over the `gold` layer, ported from banhmi with
[port-fit review conclusions](#port-review-summary) driving what shipped and what was dropped.
Schema in [`SCHEMA.md`](SCHEMA.md); pipeline + stack in [`../ARCHITECTURE.md`](../ARCHITECTURE.md).

## Shape

- **Dense arm:** Qwen3-Embedding-0.6B ONNX, 1024-d cosine, exact scan (not HNSW — 3.4k rows,
  ~2 ms; re-evaluate at 10k+).
- **Sparse arm:** BM25 `sparsevec` (2^20 hashing trick), English citation-aware tokenizer
  (lowercase + alnum split; citation tokens like `AC-2(3)`, `A&A`, `I&S` kept intact).
- **Fusion:** RRF (`rrf_k=60`); top_k=8, vector_k=100, bm25_k=100, doc_cap=0.
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
- **Query-time embed:** local ONNX in-process (requires `-tags onnx` build + model at
  `~/.cache/banhmi/qwen3-embedding`).

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

## First baseline

50-query golden set (citation-keyed, no licensed text), 10 citation schemes.

| Metric | Value | Floor |
|--------|-------|-------|
| recall@8 | 57.8% | 55% |
| MRR@8 | 32.3% | 30% |
| current-version | 100% | 98% |
| abstention | 90% | 88% |

## Known gaps

- **No score-floor abstention** — 5/5 OOS queries return hits instead of abstaining
  (no score threshold; M3 follow-up).
- **Short-chunk framework recall weak** — ISO/SOC2/PCI one-liner controls lack signal for both
  dense and sparse arms.
- **RRF constants untuned** — starting values from port review, not eval-optimized.
- **Bare-numeric citation ambiguity** — `5.1` matches 4+ schemes; resolved only by ranking
  when no framework filter is set.
