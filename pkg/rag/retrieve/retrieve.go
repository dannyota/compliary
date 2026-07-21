// Package retrieve is compliary's hybrid retrieval core. It runs dense (pgvector
// cosine exact scan) and BM25 (sparsevec dot product) arms, fuses results with
// Reciprocal Rank Fusion, and hydrates hits with framework/citation metadata.
//
// Differences from banhmi:
//   - Filters on config.framework_version.is_current (not validity_period).
//   - Citation-shaped queries get a direct citation_norm lookup arm (boosted).
//   - Exact scan only at 3.4k chunks (no HNSW path).
//   - No rollup, section-aggregate, diacritic restoration, or VN routing.
//   - No document relations (compliary uses cross-framework mappings, served via MCP).
//
// The arms use pgvector distance operators (<=> dense, <#> sparsevec BM25) and
// framework-version filters assembled into per-query CTEs. Raw, parameterized
// pgx runs against the pool (sqlc cannot model these).
package retrieve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"danny.vn/compliary/pkg/eval"
	"danny.vn/compliary/pkg/rag/embed"
	"danny.vn/compliary/pkg/rag/lexical"
)

// Defaults applied when a SearchOpts field is zero. A bare Search(ctx, q,
// SearchOpts{}) call must be sensible.
const (
	defaultTopK      = 8
	defaultVectorK   = 50
	defaultBM25K     = 50
	defaultRRFK      = 20
	defaultDocCap    = 0   // no cap — semantics differ from banhmi; eval decides
	defaultLexWeight = 0.5 // BM25 arm RRF weight relative to vector arm
)

// Compile-time check: method drift against the eval harness contract must
// fail here, not at the cmd/eval wiring point.
var _ eval.Retriever = (*Retriever)(nil)

// Retriever runs hybrid retrieval over the compliary corpus. It satisfies
// eval.Retriever.
type Retriever struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder // nil => vector arm skipped, BM25-only
	log      *slog.Logger

	// abstainFloor is the raw-cosine abstention threshold (0 = disabled);
	// see SetAbstainFloor.
	abstainFloor float64

	// frameworkScheme maps framework_code -> citation_scheme, loaded from
	// config.framework at construction time.
	frameworkScheme map[string]string
}

// New builds a Retriever. embedder may be nil (BM25-only). log may be nil.
func New(pool *pgxpool.Pool, embedder embed.Embedder, log *slog.Logger) (*Retriever, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	r := &Retriever{
		pool:     pool,
		embedder: embedder,
		log:      log,
	}
	// Load citation scheme map from config.framework.
	schemes, err := loadFrameworkSchemes(context.Background(), pool)
	if err != nil {
		return nil, fmt.Errorf("retrieve: load framework schemes: %w", err)
	}
	r.frameworkScheme = schemes

	// Dense-arm parity guard: the vector arm filters gold.chunk_embedding on
	// exact model-string equality, so an embedder whose Model() doesn't match
	// the stored rows silently retrieves nothing and the whole deployment
	// degrades to BM25-only (a real production incident, 2026-07-21 — the
	// server was configured with an org-prefixed model name). Warn loudly at
	// construction rather than fail: an empty corpus is legitimate.
	if embedder != nil {
		var n int64
		err := pool.QueryRow(context.Background(),
			"SELECT count(*) FROM gold.chunk_embedding WHERE model = $1", embedder.Model()).Scan(&n)
		switch {
		case err != nil:
			log.Warn("retrieve: cannot verify embedding-model parity", "err", err)
		case n == 0:
			log.Warn("retrieve: NO stored embeddings match the query embedder's model — the dense arm will retrieve nothing and search runs BM25-only",
				"query_model", embedder.Model(), "hint", "use embed.CanonicalModel")
		default:
			log.Info("retrieve: dense arm ready", "model", embedder.Model(), "embeddings", n)
		}
	}
	return r, nil
}

func loadFrameworkSchemes(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, "SELECT code, citation_scheme FROM config.framework")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var code, scheme string
		if err := rows.Scan(&code, &scheme); err != nil {
			return nil, err
		}
		m[code] = scheme
	}
	return m, rows.Err()
}

// resolved holds the per-call knobs after merging SearchOpts with defaults.
type resolved struct {
	mode              eval.SearchMode
	topK              int
	vectorK           int
	bm25K             int
	rrfK              int
	docCap            int
	lexWeight         float64
	currentOnly       bool
	surfaceNonCurrent bool
	framework         string
	versionLabel      string
	includeWithdrawn  bool
}

func (r *Retriever) resolve(opts eval.SearchOpts) (resolved, error) {
	pick := func(o, d int) int {
		if o > 0 {
			return o
		}
		return d
	}
	currentOnly := true
	surfaceNonCurrent := true
	if opts.CurrentOnly != nil {
		currentOnly = *opts.CurrentOnly
		surfaceNonCurrent = false
	}
	mode := opts.Mode
	if mode == "" {
		if r.embedder != nil {
			mode = eval.SearchModeHybrid
		} else {
			mode = eval.SearchModeBM25
		}
	} else {
		var err error
		mode, err = eval.ParseSearchMode(string(mode))
		if err != nil {
			return resolved{}, err
		}
	}
	// A framework or version pin disables the non-current pass.
	if opts.Framework != "" || opts.VersionLabel != "" {
		surfaceNonCurrent = false
	}
	lexWeight := opts.LexWeight
	if lexWeight <= 0 {
		lexWeight = defaultLexWeight
	}
	return resolved{
		mode:              mode,
		topK:              pick(opts.TopK, defaultTopK),
		vectorK:           pick(opts.VectorK, defaultVectorK),
		bm25K:             pick(opts.BM25K, defaultBM25K),
		rrfK:              pick(opts.RRFK, defaultRRFK),
		docCap:            pick(opts.DocCap, defaultDocCap),
		lexWeight:         lexWeight,
		currentOnly:       currentOnly,
		surfaceNonCurrent: surfaceNonCurrent,
		framework:         opts.Framework,
		versionLabel:      opts.VersionLabel,
		includeWithdrawn:  opts.IncludeWithdrawn,
	}, nil
}

// Search returns ranked hits. Satisfies eval.Retriever.
func (r *Retriever) Search(ctx context.Context, query string, opts eval.SearchOpts) ([]eval.Hit, error) {
	ev, err := r.SearchEvidence(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	return ev.Hits, nil
}

// SearchEvidence returns ranked hits plus gap/abstain signals.
func (r *Retriever) SearchEvidence(ctx context.Context, query string, opts eval.SearchOpts) (eval.Evidence, error) {
	hits, err := r.searchHits(ctx, query, opts)
	if err != nil {
		return eval.Evidence{}, err
	}
	ev := eval.Evidence{Hits: hits}
	if len(hits) == 0 {
		ev.Abstain = true
		return ev, nil
	}
	// TopScore reports the highest RRF-fused score among non-pinned hits.
	// Pinned citation hits carry a synthetic Score of 1.0 which is misleading
	// for consumers judging retrieval quality; when only pinned hits exist,
	// TopScore stays 0.
	for _, h := range hits {
		if h.Score < 1.0 && h.Score > ev.TopScore {
			ev.TopScore = h.Score
		}
	}
	applyAbstainFloor(&ev, r.abstainFloor)
	return ev, nil
}

// applyAbstainFloor fills TopCosine and applies raw-cosine score-floor
// abstention to ev. The RRF-fused score is a rank artifact with no absolute
// scale, but cosine similarity is comparable across queries, so the floor
// compares the best raw cosine across hits — the top fused hit can be BM25-led
// (Similarity 0), so all hits are scanned. Only meaningful when the dense arm
// ran: BM25-only mode has no cosine and must not abstain on it.
func applyAbstainFloor(ev *eval.Evidence, floor float64) {
	denseRan := false
	for _, h := range ev.Hits {
		if h.VectorRank > 0 {
			denseRan = true
		}
		if h.Similarity > ev.TopCosine {
			ev.TopCosine = h.Similarity
		}
	}
	if floor > 0 && denseRan && ev.TopCosine < floor {
		ev.Abstain = true
		ev.Gaps = append(ev.Gaps, eval.Gap{
			Kind: "low_confidence",
			Message: fmt.Sprintf("best vector similarity %.4f is below the configured floor %.4f; the query may be outside the corpus scope",
				ev.TopCosine, floor),
			BlocksAnswer: true,
		})
	}
}

// SetAbstainFloor sets the raw-cosine abstention floor (0 disables). Callers
// load the value from the config.setting `search_abstain_floor` row.
func (r *Retriever) SetAbstainFloor(f float64) { r.abstainFloor = f }

// searchHits runs the full retrieval pipeline.
func (r *Retriever) searchHits(ctx context.Context, query string, opts eval.SearchOpts) ([]eval.Hit, error) {
	if query == "" {
		return nil, nil
	}
	res, err := r.resolve(opts)
	if err != nil {
		return nil, fmt.Errorf("retrieve: resolve opts: %w", err)
	}

	// Citation routing: detect citation-shaped queries and do a direct DB
	// lookup. Pinned hits get rank 0, score 1.0 so they lead the result.
	var pinnedHits []eval.Hit
	citMatches := MatchCitation(query, res.framework, r.frameworkScheme)
	if len(citMatches) > 0 {
		pinnedHits, err = r.citationLookup(ctx, citMatches, res)
		if err != nil {
			r.log.Warn("retrieve: citation lookup failed, falling through",
				"err", err, "matches", len(citMatches))
		}
	}

	// Vector arm.
	var vectorList []ranked
	var queryVec *pgvector.Vector
	if res.mode != eval.SearchModeBM25 {
		if r.embedder == nil {
			if res.mode == eval.SearchModeVector {
				return nil, fmt.Errorf("retrieve: vector mode requested but no embedder is configured")
			}
			r.log.Debug("retrieve: no embedder, running BM25-only")
		} else {
			vecs, err := r.embedder.Embed(ctx, []string{embed.FormatQuery(query)})
			if err != nil {
				return nil, fmt.Errorf("retrieve: embed query: %w", err)
			}
			if len(vecs) != 1 || vecs[0] == nil {
				return nil, fmt.Errorf("retrieve: embedder returned %d vectors for one query", len(vecs))
			}
			qv := pgvector.NewVector(vecs[0])
			queryVec = &qv
			vectorList, err = r.vectorArmExact(ctx, qv, res, false)
			if err != nil {
				return nil, fmt.Errorf("retrieve: vector arm: %w", err)
			}
		}
	}

	// Lexical arm.
	var bm25List []ranked
	if res.mode != eval.SearchModeVector {
		bm25List, err = r.sparseArm(ctx, query, res)
		if err != nil {
			return nil, fmt.Errorf("retrieve: lexical arm: %w", err)
		}
	}

	// Fusion. When we have citation pinned hits, boost the lexical weight
	// so citation token overlap drives the supporting results.
	lexWeight := res.lexWeight
	if len(pinnedHits) > 0 && lexWeight < 1.5 {
		lexWeight = 1.5 // citation-led: boost lexical for related results
	}
	fused := fuseRRF(vectorList, bm25List, res.rrfK, lexWeight)

	// Hydrate fused candidates.
	hydrateK := res.topK
	if len(pinnedHits) > 0 {
		hydrateK = res.topK - len(pinnedHits)
		if hydrateK < 1 {
			hydrateK = 1
		}
	}
	if len(fused) > hydrateK {
		fused = fused[:hydrateK]
	}

	var hits []eval.Hit
	if len(fused) > 0 {
		hits, err = r.hydrate(ctx, fused)
		if err != nil {
			return nil, fmt.Errorf("retrieve: hydrate: %w", err)
		}
	}

	// Doc cap.
	if res.docCap > 0 {
		hits = capPerFramework(hits, res.docCap, hydrateK)
	} else if len(hits) > hydrateK {
		hits = hits[:hydrateK]
	}

	// Merge pinned hits ahead of fused results, deduplicating.
	if len(pinnedHits) > 0 {
		hits = mergePinned(pinnedHits, hits, res.topK)
	}

	// Non-current pass: surface a small set of non-current version matches.
	if res.surfaceNonCurrent && queryVec != nil {
		nc, err := r.nonCurrentHits(ctx, *queryVec, res)
		if err != nil {
			r.log.Warn("retrieve: non-current pass failed", "err", err)
		} else {
			hits = appendNonCurrent(hits, nc)
		}
	}

	r.log.Debug("retrieve: search complete",
		"query_len", len(query),
		"mode", res.mode,
		"vector_hits", len(vectorList),
		"bm25_hits", len(bm25List),
		"fused", len(fused),
		"pinned", len(pinnedHits),
		"returned", len(hits),
	)
	return hits, nil
}

// --- Version filter CTE ---

// buildVersionFilterCTE constructs the version filter CTE. It joins
// silver.document to config.framework_version to get the document_ids whose
// framework version matches the filter. Default: is_current only.
func buildVersionFilterCTE(res resolved, startParam int) (string, []any) {
	var conds []string
	var args []any
	p := startParam

	if res.framework != "" {
		conds = append(conds, fmt.Sprintf("d.framework_code = $%d", p))
		args = append(args, res.framework)
		p++
	}
	if res.versionLabel != "" {
		conds = append(conds, fmt.Sprintf("d.version_label = $%d", p))
		args = append(args, res.versionLabel)
		p++
	} else if res.currentOnly {
		conds = append(conds, "fv.is_current = true")
	}

	if len(conds) == 0 {
		// No filter at all (CurrentOnly=false, no framework/version pin).
		return "", nil
	}

	cte := fmt.Sprintf(`
WITH version_filter AS (
    SELECT d.id AS document_id
    FROM silver.document d
    JOIN config.framework_version fv
      ON fv.framework_code = d.framework_code
     AND fv.version_label = d.version_label
    WHERE %s
)`, strings.Join(conds, "\n      AND "))
	return cte, args
}

// --- Vector arm (exact scan) ---

func (r *Retriever) vectorArmExact(ctx context.Context, qv pgvector.Vector, res resolved, nonCurrent bool) ([]ranked, error) {
	model := r.embedder.Model()
	args := []any{qv, model, res.vectorK}

	statusPred := "sc.status = 'active'"
	if res.includeWithdrawn {
		statusPred = "sc.status IN ('active', 'withdrawn')"
	}

	filteredBody := fmt.Sprintf(`
SELECT c.id, (e.embedding <=> $1)::float8
FROM gold.chunk_embedding e
JOIN gold.chunk c ON c.id = e.chunk_id
JOIN silver.control sc ON sc.id = c.control_id
WHERE e.model = $2
  AND %s
  AND sc.document_id IN (SELECT document_id FROM version_filter)
ORDER BY e.embedding <=> $1, c.id
LIMIT $3`, statusPred)

	var sql string
	if nonCurrent {
		// Non-current pass: invert the current filter.
		cte, fargs := buildNonCurrentCTE(res, len(args)+1)
		args = append(args, fargs...)
		sql = cte + filteredBody
	} else {
		cte, fargs := buildVersionFilterCTE(res, len(args)+1)
		if cte == "" {
			sql = fmt.Sprintf(`
SELECT c.id, (e.embedding <=> $1)::float8
FROM gold.chunk_embedding e
JOIN gold.chunk c ON c.id = e.chunk_id
JOIN silver.control sc ON sc.id = c.control_id
WHERE e.model = $2
  AND %s
ORDER BY e.embedding <=> $1, c.id
LIMIT $3`, statusPred)
		} else {
			args = append(args, fargs...)
			sql = cte + filteredBody
		}
	}

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("vector query: %w", err)
	}
	return scanRankedWithDistance(rows)
}

// buildNonCurrentCTE builds a CTE selecting documents from non-current versions.
func buildNonCurrentCTE(res resolved, startParam int) (string, []any) {
	var conds []string
	var args []any
	p := startParam

	if res.framework != "" {
		conds = append(conds, fmt.Sprintf("d.framework_code = $%d", p))
		args = append(args, res.framework)
		p++
	}
	// Non-current: explicitly NOT current.
	conds = append(conds, "fv.is_current = false")

	cte := fmt.Sprintf(`
WITH version_filter AS (
    SELECT d.id AS document_id
    FROM silver.document d
    JOIN config.framework_version fv
      ON fv.framework_code = d.framework_code
     AND fv.version_label = d.version_label
    WHERE %s
)`, strings.Join(conds, "\n      AND "))
	return cte, args
}

// --- Lexical (BM25) arm ---

func (r *Retriever) sparseArm(ctx context.Context, query string, res resolved) ([]ranked, error) {
	qv := lexical.QueryVector(query)
	args := []any{qv, res.bm25K}

	statusPred := "sc.status = 'active'"
	if res.includeWithdrawn {
		statusPred = "sc.status IN ('active', 'withdrawn')"
	}

	filteredBody := fmt.Sprintf(`
SELECT c.id, (c.content_sparse <#> $1::sparsevec) AS neg_ip
FROM gold.chunk c
JOIN silver.control sc ON sc.id = c.control_id
WHERE c.content_sparse IS NOT NULL
  AND %s
  AND (c.content_sparse <#> $1::sparsevec) < 0
  AND sc.document_id IN (SELECT document_id FROM version_filter)
ORDER BY c.content_sparse <#> $1::sparsevec
LIMIT $2`, statusPred)

	cte, fargs := buildVersionFilterCTE(res, len(args)+1)
	var sql string
	if cte == "" {
		sql = fmt.Sprintf(`
SELECT c.id, (c.content_sparse <#> $1::sparsevec) AS neg_ip
FROM gold.chunk c
JOIN silver.control sc ON sc.id = c.control_id
WHERE c.content_sparse IS NOT NULL
  AND %s
  AND (c.content_sparse <#> $1::sparsevec) < 0
ORDER BY c.content_sparse <#> $1::sparsevec
LIMIT $2`, statusPred)
	} else {
		args = append(args, fargs...)
		sql = cte + filteredBody
	}

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("bm25 query: %w", err)
	}
	return scanRankedBM25(rows)
}

// --- Row scanners ---

func scanRankedWithDistance(rows pgx.Rows) ([]ranked, error) {
	defer rows.Close()
	var out []ranked
	rank := 0
	for rows.Next() {
		var id int64
		var dist float64
		if err := rows.Scan(&id, &dist); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rank++
		out = append(out, ranked{chunkID: id, rank: rank, similarity: 1 - dist})
	}
	return out, rows.Err()
}

func scanRankedBM25(rows pgx.Rows) ([]ranked, error) {
	defer rows.Close()
	var out []ranked
	rank := 0
	for rows.Next() {
		var id int64
		var negIP float64
		if err := rows.Scan(&id, &negIP); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rank++
		out = append(out, ranked{chunkID: id, rank: rank, bm25Score: -negIP})
	}
	return out, rows.Err()
}

// --- Citation lookup ---

// citationLookup does a direct DB lookup for citation-shaped queries. Returns
// pinned hits with Score=1.0 so they lead the result set.
func (r *Retriever) citationLookup(ctx context.Context, matches []CitationMatch, res resolved) ([]eval.Hit, error) {
	if len(matches) == 0 {
		return nil, nil
	}

	// Collect unique citation_norm values to look up.
	norms := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, m := range matches {
		norm := strings.ToUpper(m.Citation)
		if !seen[norm] {
			seen[norm] = true
			norms = append(norms, norm)
		}
	}

	// Build query with optional framework/version filter.
	args := []any{norms}
	var conds []string
	conds = append(conds, "upper(sc.citation_norm) = ANY($1)")
	p := 2

	if res.framework != "" {
		conds = append(conds, fmt.Sprintf("d.framework_code = $%d", p))
		args = append(args, res.framework)
		p++
	}
	if res.versionLabel != "" {
		conds = append(conds, fmt.Sprintf("d.version_label = $%d", p))
		args = append(args, res.versionLabel)
		p++
	} else if res.currentOnly {
		conds = append(conds, `EXISTS (
            SELECT 1 FROM config.framework_version fv
            WHERE fv.framework_code = d.framework_code
              AND fv.version_label = d.version_label
              AND fv.is_current = true
        )`)
	}

	statusPred := "sc.status = 'active'"
	if res.includeWithdrawn {
		statusPred = "sc.status IN ('active', 'withdrawn')"
	}

	sql := fmt.Sprintf(`
SELECT
    c.id,
    sc.document_id,
    d.framework_code,
    d.version_label,
    sc.citation,
    sc.citation_norm,
    COALESCE(c.context_prefix, ''),
    c.content,
    fv.is_current
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN gold.chunk c ON c.control_id = sc.id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code
 AND fv.version_label = d.version_label
WHERE %s
  AND %s
ORDER BY fv.is_current DESC, d.framework_code, d.version_label, sc.citation_norm, c.ordinal
LIMIT 16`, strings.Join(conds, "\n  AND "), statusPred)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("citation lookup query: %w", err)
	}
	defer rows.Close()

	var hits []eval.Hit
	for rows.Next() {
		var h eval.Hit
		if err := rows.Scan(
			&h.ChunkID,
			&h.DocumentID,
			&h.FrameworkCode,
			&h.VersionLabel,
			&h.Citation,
			&h.CitationNorm,
			&h.ContextPrefix,
			&h.Content,
			&h.IsCurrent,
		); err != nil {
			return nil, fmt.Errorf("citation lookup scan: %w", err)
		}
		h.Score = 1.0
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// --- Hydration ---

// hydrate loads citation metadata for the fused hits.
func (r *Retriever) hydrate(ctx context.Context, fused []fusedHit) ([]eval.Hit, error) {
	ids := make([]int64, len(fused))
	for i, f := range fused {
		ids[i] = f.chunkID
	}

	const sql = `
SELECT
    c.id,
    sc.document_id,
    d.framework_code,
    d.version_label,
    sc.citation,
    sc.citation_norm,
    COALESCE(c.context_prefix, ''),
    c.content,
    fv.is_current
FROM gold.chunk c
JOIN silver.control sc ON sc.id = c.control_id
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code
 AND fv.version_label = d.version_label
WHERE c.id = ANY($1)`

	rows, err := r.pool.Query(ctx, sql, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate query: %w", err)
	}
	defer rows.Close()

	type meta struct {
		documentID    int64
		frameworkCode string
		versionLabel  string
		citation      string
		citationNorm  string
		contextPrefix string
		content       string
		isCurrent     bool
	}
	byID := make(map[int64]meta, len(fused))
	for rows.Next() {
		var id int64
		var m meta
		if err := rows.Scan(
			&id,
			&m.documentID,
			&m.frameworkCode,
			&m.versionLabel,
			&m.citation,
			&m.citationNorm,
			&m.contextPrefix,
			&m.content,
			&m.isCurrent,
		); err != nil {
			return nil, fmt.Errorf("hydrate scan: %w", err)
		}
		byID[id] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hydrate rows: %w", err)
	}

	hits := make([]eval.Hit, 0, len(fused))
	for _, f := range fused {
		m, ok := byID[f.chunkID]
		if !ok {
			r.log.Warn("retrieve: ranked chunk missing on hydrate", "chunk_id", f.chunkID)
			continue
		}
		hits = append(hits, eval.Hit{
			ChunkID:       f.chunkID,
			DocumentID:    m.documentID,
			FrameworkCode: m.frameworkCode,
			VersionLabel:  m.versionLabel,
			Citation:      m.citation,
			CitationNorm:  m.citationNorm,
			ContextPrefix: m.contextPrefix,
			Content:       m.content,
			Score:         f.score,
			Similarity:    f.similarity,
			BM25Score:     f.bm25Score,
			VectorRank:    f.vectorRank,
			BM25Rank:      f.bm25Rank,
			IsCurrent:     m.isCurrent,
		})
	}
	return hits, nil
}

// --- Non-current pass ---

const nonCurrentCap = 3

func (r *Retriever) nonCurrentHits(ctx context.Context, queryVec pgvector.Vector, res resolved) ([]eval.Hit, error) {
	list, err := r.vectorArmExact(ctx, queryVec, res, true)
	if err != nil {
		return nil, fmt.Errorf("non-current vector arm: %w", err)
	}
	fused := fuseRRF(list, nil, res.rrfK, 1.0)
	limit := nonCurrentCap
	if res.topK < limit {
		limit = res.topK
	}
	if len(fused) > limit {
		fused = fused[:limit]
	}
	hits, err := r.hydrate(ctx, fused)
	if err != nil {
		return nil, fmt.Errorf("non-current hydrate: %w", err)
	}
	// Best hit per framework (not per document — one chunk per control).
	return bestHitPerFramework(hits), nil
}

// --- Helpers ---

// capPerFramework limits how many top-k slots one framework may occupy.
func capPerFramework(hits []eval.Hit, frameworkCap, topK int) []eval.Hit {
	if topK <= 0 || len(hits) == 0 {
		return hits
	}
	selected := make([]eval.Hit, 0, topK)
	var demoted []eval.Hit
	perFw := make(map[string]int)
	for _, h := range hits {
		if len(selected) == topK {
			break
		}
		if perFw[h.FrameworkCode] >= frameworkCap {
			demoted = append(demoted, h)
			continue
		}
		perFw[h.FrameworkCode]++
		selected = append(selected, h)
	}
	for _, h := range demoted {
		if len(selected) == topK {
			break
		}
		selected = append(selected, h)
	}
	return selected
}

func bestHitPerFramework(hits []eval.Hit) []eval.Hit {
	out := make([]eval.Hit, 0, len(hits))
	seen := make(map[string]bool, len(hits))
	for _, h := range hits {
		key := h.FrameworkCode + "|" + h.VersionLabel
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

func mergePinned(pinned, fused []eval.Hit, topK int) []eval.Hit {
	pinnedIDs := make(map[int64]bool, len(pinned))
	for _, h := range pinned {
		pinnedIDs[h.ChunkID] = true
	}
	out := make([]eval.Hit, 0, topK)
	out = append(out, pinned...)
	for _, h := range fused {
		if len(out) >= topK {
			break
		}
		if pinnedIDs[h.ChunkID] {
			continue
		}
		out = append(out, h)
	}
	return out
}

func appendNonCurrent(current, nonCurrent []eval.Hit) []eval.Hit {
	if len(nonCurrent) == 0 {
		return current
	}
	seen := make(map[int64]bool, len(current))
	for _, h := range current {
		seen[h.ChunkID] = true
	}
	for _, h := range nonCurrent {
		if !seen[h.ChunkID] {
			current = append(current, h)
			seen[h.ChunkID] = true
		}
	}
	return current
}
