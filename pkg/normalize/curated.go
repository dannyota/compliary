// curated.go consumes operator-authored cross-framework edges from
// config.curated_mapping and emits them as silver.control_mapping edges.
//
// Unlike the ISO structural derivation (mapedges.go) or the publisher-workbook
// producers (cismap.go, olir.go), these edges carry no publisher authority:
// each row is the operator's own analysis of both endpoints' text, seeded from
// deploy/seed/curated_mapping.csv under mapping_source 'operator'. The primary
// use is the two TCVN corpora, whose publications ship no crosswalk table.
//
// The from side must resolve to an existing silver.control (business key
// framework_code + version_label + citation_norm); rows whose from-control is
// not in the corpus are counted and reported as Unresolved — never silently
// dropped. The to side resolves lazily like any other edge.
package normalize

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// CuratedEdge is one operator-curated mapping row from config.curated_mapping.
// ToVersion is empty when the row does not pin a target version.
type CuratedEdge struct {
	FromFramework string
	FromVersion   string
	FromCitation  string
	ToFramework   string
	ToVersion     string
	ToCitation    string
	Relationship  string
	MappingSource string
	Note          string
}

// CuratedEdgesSummary reports the outcome of the curated-edge pass.
type CuratedEdgesSummary struct {
	Total      int // curated rows loaded from config
	Emitted    int // from-control resolved → edge upserted
	Unresolved int // from-control not found in the corpus
	Resolved   int // to_control_id filled after this pass (all sources)
}

// fromKey is the business key used to match a curated row's from side to a
// stored control. Upper-cased so citation casing (e.g. PE vs pe) never bites.
func fromKey(framework, version, citation string) string {
	return strings.ToUpper(framework + "|" + version + "|" + citation)
}

// EmitCuratedEdges loads config.curated_mapping, resolves each row's from side
// to a silver.control, and upserts a silver.control_mapping edge. Rows whose
// from-control is absent are counted in Unresolved. It then runs the standard
// lazy to-side resolution pass.
func EmitCuratedEdges(
	ctx context.Context,
	pool *pgxpool.Pool,
	silverQ MapEdgesQuerier,
	log *slog.Logger,
) (CuratedEdgesSummary, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	rows, err := loadCuratedEdges(ctx, pool)
	if err != nil {
		return CuratedEdgesSummary{}, fmt.Errorf("load curated edges: %w", err)
	}
	if len(rows) == 0 {
		log.Info("mapedges: no curated edges seeded — skipping")
		return CuratedEdgesSummary{}, nil
	}

	fromIndex, err := loadFromControlIndex(ctx, pool, rows)
	if err != nil {
		return CuratedEdgesSummary{}, fmt.Errorf("load curated from-index: %w", err)
	}

	params, unresolved := buildCuratedUpserts(rows, fromIndex)

	for _, p := range params {
		if err := silverQ.UpsertControlMapping(ctx, p); err != nil {
			return CuratedEdgesSummary{}, fmt.Errorf("curated: upsert → %s %s: %w", p.ToFrameworkCode, p.ToCitationNorm, err)
		}
	}

	for _, u := range unresolved {
		log.Warn("mapedges: curated from-control not in corpus — edge skipped",
			"from_framework", u.FromFramework, "from_version", u.FromVersion, "from_citation", u.FromCitation,
			"to_framework", u.ToFramework, "to_citation", u.ToCitation)
	}

	sum := CuratedEdgesSummary{
		Total:      len(rows),
		Emitted:    len(params),
		Unresolved: len(unresolved),
	}
	log.Info("mapedges: curated edges emitted", "total", sum.Total, "emitted", sum.Emitted, "unresolved", sum.Unresolved)

	resolved, err := silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return sum, fmt.Errorf("curated: resolve mappings: %w", err)
	}
	sum.Resolved = int(resolved)
	log.Info("mapedges: curated mappings resolved", "resolved", sum.Resolved)

	return sum, nil
}

// buildCuratedUpserts is the pure matching step: for each curated row it looks
// up the from-control ID in fromIndex and builds an UpsertControlMappingParams,
// or records the row as unresolved. Order is preserved so counts are stable.
func buildCuratedUpserts(rows []CuratedEdge, fromIndex map[string]int64) (params []dbsilver.UpsertControlMappingParams, unresolved []CuratedEdge) {
	for _, r := range rows {
		id, ok := fromIndex[fromKey(r.FromFramework, r.FromVersion, r.FromCitation)]
		if !ok {
			unresolved = append(unresolved, r)
			continue
		}
		var toVer *string
		if r.ToVersion != "" {
			v := r.ToVersion
			toVer = &v
		}
		params = append(params, dbsilver.UpsertControlMappingParams{
			FromControlID:     id,
			ToFrameworkCode:   r.ToFramework,
			ToVersionLabel:    toVer,
			ToCitationNorm:    r.ToCitation,
			MappingSourceCode: r.MappingSource,
			Relationship:      r.Relationship,
			ProvenanceDetail:  r.Note,
		})
	}
	return params, unresolved
}

// loadCuratedEdges reads all rows from config.curated_mapping.
func loadCuratedEdges(ctx context.Context, pool *pgxpool.Pool) ([]CuratedEdge, error) {
	const q = `
SELECT from_framework_code, from_version_label, from_citation_norm,
       to_framework_code, to_version_label, to_citation_norm,
       relationship, mapping_source_code, note
FROM config.curated_mapping
ORDER BY from_framework_code, from_version_label, from_citation_norm, to_framework_code, to_citation_norm`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query curated_mapping: %w", err)
	}
	defer rows.Close()

	var out []CuratedEdge
	for rows.Next() {
		var e CuratedEdge
		var toVer *string
		if err := rows.Scan(&e.FromFramework, &e.FromVersion, &e.FromCitation,
			&e.ToFramework, &toVer, &e.ToCitation,
			&e.Relationship, &e.MappingSource, &e.Note); err != nil {
			return nil, fmt.Errorf("scan curated_mapping: %w", err)
		}
		if toVer != nil {
			e.ToVersion = *toVer
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// loadFromControlIndex loads silver.control IDs for every distinct
// (from_framework, from_version) pair referenced by the curated rows, keyed by
// fromKey. One query per distinct pair keeps the load small and index-friendly.
func loadFromControlIndex(ctx context.Context, pool *pgxpool.Pool, rows []CuratedEdge) (map[string]int64, error) {
	type pair struct{ framework, version string }
	seen := map[pair]bool{}
	var pairs []pair
	for _, r := range rows {
		p := pair{r.FromFramework, r.FromVersion}
		if !seen[p] {
			seen[p] = true
			pairs = append(pairs, p)
		}
	}

	index := map[string]int64{}
	const q = `
SELECT sc.citation_norm, sc.id
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
WHERE d.framework_code = $1 AND d.version_label = $2`
	for _, p := range pairs {
		crows, err := pool.Query(ctx, q, p.framework, p.version)
		if err != nil {
			return nil, fmt.Errorf("query controls %s/%s: %w", p.framework, p.version, err)
		}
		for crows.Next() {
			var norm string
			var id int64
			if err := crows.Scan(&norm, &id); err != nil {
				crows.Close()
				return nil, fmt.Errorf("scan control %s/%s: %w", p.framework, p.version, err)
			}
			index[fromKey(p.framework, p.version, norm)] = id
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return nil, err
		}
	}
	return index, nil
}
