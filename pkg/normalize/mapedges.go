// mapedges.go generates deterministic structural equivalence edges between
// ISO-family controls derived from their numbering structure, not from a
// publisher mapping table.
//
// ISO 27001:2022 Annex A control A.x.y is structurally identical to
// ISO 27002:2022 control x.y — the same 93 controls in both publications,
// one as a normative checklist (27001 Annex A) and the other as the detailed
// implementation guidance (27002). The relationship is 'equivalent' with
// bidirectional edges.
//
// ISO 27017:2015 and ISO 27018:2019 use ISO 27002:2013 numbering (deep x.y.z
// hierarchy), not the 27002:2022 flat numbering. Since compliary does not
// ingest 27002:2013, these mappings cannot be derived structurally with
// certainty and are intentionally omitted. ISO 27018:2025 realigns to
// 27002:2022, but the :2019 edition in the corpus predates that change.
package normalize

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// MapEdgesSummary holds counts from the mapedges stage.
type MapEdgesSummary struct {
	Emitted  int
	Resolved int
}

// MapEdgesQuerier is the subset of dbsilver.Querier needed by the mapedges
// stage. It shares the UpsertControlMapping and ResolveControlMappings methods
// with the normalize stage's SilverQuerier — the same proven write path.
type MapEdgesQuerier interface {
	UpsertControlMapping(ctx context.Context, arg dbsilver.UpsertControlMappingParams) error
	ResolveControlMappings(ctx context.Context) (int64, error)
}

const mappingSourceISO = "iso-structural"

// EmitISOStructuralEdges generates the 27001 A.x.y ≡ 27002 x.y equivalence
// edges. It reads the 93 annex-controls from silver.control (27001:2022),
// derives the corresponding 27002:2022 citation by stripping "A.", and emits
// bidirectional 'equivalent' mapping edges.
//
// The mapping_source 'iso-structural' must already be seeded in
// config.mapping_source before calling this function.
func EmitISOStructuralEdges(
	ctx context.Context,
	pool *pgxpool.Pool,
	silverQ MapEdgesQuerier,
	log *slog.Logger,
) (MapEdgesSummary, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	// Load 27001:2022 annex-controls with their DB IDs.
	controls, err := loadAnnexControls(ctx, pool)
	if err != nil {
		return MapEdgesSummary{}, fmt.Errorf("load annex controls: %w", err)
	}
	if len(controls) == 0 {
		log.Info("mapedges: no ISO 27001:2022 annex-controls found — skipping")
		return MapEdgesSummary{}, nil
	}

	var emitted int
	for _, ac := range controls {
		// A.x.y → x.y
		target := strings.TrimPrefix(ac.citationNorm, "A.")
		if target == ac.citationNorm {
			continue // Not an A.x.y pattern — skip.
		}

		// 27001 A.x.y → 27002 x.y
		err := silverQ.UpsertControlMapping(ctx, dbsilver.UpsertControlMappingParams{
			FromControlID:     ac.id,
			ToFrameworkCode:   "iso27002",
			ToVersionLabel:    strPtr("2022"),
			ToCitationNorm:    target,
			MappingSourceCode: mappingSourceISO,
			Relationship:      "equivalent",
			ProvenanceDetail:  "derived from ISO numbering structure: 27001 Annex A control A.x.y is structurally identical to 27002 control x.y",
		})
		if err != nil {
			return MapEdgesSummary{}, fmt.Errorf("upsert mapping 27001 A.%s → 27002 %s: %w", target, target, err)
		}
		emitted++
	}

	// Reverse: 27002 x.y → 27001 A.x.y.
	controls27002, err := load27002Controls(ctx, pool)
	if err != nil {
		return MapEdgesSummary{}, fmt.Errorf("load 27002 controls: %w", err)
	}
	for _, c := range controls27002 {
		annex := "A." + c.citationNorm
		err := silverQ.UpsertControlMapping(ctx, dbsilver.UpsertControlMappingParams{
			FromControlID:     c.id,
			ToFrameworkCode:   "iso27001",
			ToVersionLabel:    strPtr("2022"),
			ToCitationNorm:    annex,
			MappingSourceCode: mappingSourceISO,
			Relationship:      "equivalent",
			ProvenanceDetail:  "derived from ISO numbering structure: 27002 control x.y is structurally identical to 27001 Annex A control A.x.y",
		})
		if err != nil {
			return MapEdgesSummary{}, fmt.Errorf("upsert mapping 27002 %s → 27001 %s: %w", c.citationNorm, annex, err)
		}
		emitted++
	}

	log.Info("mapedges: ISO structural edges emitted", "emitted", emitted)

	// Resolve all edges (including newly emitted ones).
	resolved, err := silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return MapEdgesSummary{}, fmt.Errorf("resolve mappings: %w", err)
	}
	log.Info("mapedges: mappings resolved", "resolved", resolved)

	return MapEdgesSummary{Emitted: emitted, Resolved: int(resolved)}, nil
}

type controlRef struct {
	id           int64
	citationNorm string
}

func loadAnnexControls(ctx context.Context, pool *pgxpool.Pool) ([]controlRef, error) {
	const q = `
SELECT sc.id, sc.citation_norm
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
WHERE d.framework_code = 'iso27001' AND d.version_label = '2022'
  AND sc.kind = 'annex-control'
ORDER BY sc.citation_norm`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query annex controls: %w", err)
	}
	defer rows.Close()

	var out []controlRef
	for rows.Next() {
		var c controlRef
		if err := rows.Scan(&c.id, &c.citationNorm); err != nil {
			return nil, fmt.Errorf("scan annex control: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func load27002Controls(ctx context.Context, pool *pgxpool.Pool) ([]controlRef, error) {
	const q = `
SELECT sc.id, sc.citation_norm
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
WHERE d.framework_code = 'iso27002' AND d.version_label = '2022'
  AND sc.kind = 'control'
ORDER BY sc.citation_norm`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query 27002 controls: %w", err)
	}
	defer rows.Close()

	var out []controlRef
	for rows.Next() {
		var c controlRef
		if err := rows.Scan(&c.id, &c.citationNorm); err != nil {
			return nil, fmt.Errorf("scan 27002 control: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
