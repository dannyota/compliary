// resolve.go implements enhanced mapping resolution passes that run after the
// standard ResolveControlMappings call. Two additional resolution strategies:
//
//  1. Annex-A prefix normalization for iso27001 targets: edges citing bare
//     Annex numbers ("5.26") resolve against corpus controls with the "A."
//     prefix ("A.5.26"), but only when the bare form does NOT match a
//     management clause (clauses 4-10 exist both bare and in Annex A).
//
//  2. Cross-version supersession: edges citing a version that has a
//     silver.version_relation(supersedes) row connecting it to the ingested
//     successor version resolve against the successor's controls. The edge
//     keeps its original to_version_label; provenance_detail records the
//     version hop.
package normalize

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolveAnnexPrefix resolves unresolved iso27001 mapping edges whose
// to_citation_norm is a bare Annex number (e.g. "5.26") by matching the
// corpus control with "A." + to_citation_norm (e.g. "A.5.26"). The resolution
// is guarded: a bare citation that already matches a management clause
// (kind='clause') is NOT re-resolved — it was the intended target, and the
// caller meant the management system clause, not the Annex A control.
//
// Returns the number of edges resolved.
func ResolveAnnexPrefix(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (int64, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	const q = `
UPDATE silver.control_mapping cm
SET to_control_id = c.id,
    provenance_detail = cm.provenance_detail ||
        CASE WHEN cm.provenance_detail = '' THEN '' ELSE '; ' END ||
        'annex-prefix: resolved ' || cm.to_citation_norm || ' as A.' || cm.to_citation_norm
FROM silver.control c
JOIN silver.document d ON d.id = c.document_id
WHERE cm.to_control_id IS NULL
  AND cm.to_framework_code = 'iso27001'
  AND d.framework_code = 'iso27001'
  AND d.version_label = COALESCE(cm.to_version_label, (
      SELECT fv.version_label FROM config.framework_version fv
      WHERE fv.framework_code = 'iso27001' AND fv.is_current
  ))
  AND c.citation_norm = 'A.' || cm.to_citation_norm
  AND c.kind = 'annex-control'
  -- Guard: the bare citation must NOT match a management clause in the same
  -- document version. If it does, the edge targets the clause, not the Annex
  -- control, and should stay unresolved (the clause already matched in the
  -- standard pass, or the target is genuinely a different numbering).
  AND NOT EXISTS (
      SELECT 1 FROM silver.control mc
      JOIN silver.document md ON md.id = mc.document_id
      WHERE md.framework_code = 'iso27001'
        AND md.version_label = d.version_label
        AND mc.citation_norm = cm.to_citation_norm
        AND mc.kind = 'clause'
  )`

	tag, err := pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("resolve annex prefix: %w", err)
	}
	n := tag.RowsAffected()
	log.Info("resolve: annex-prefix normalization", "resolved", n)
	return n, nil
}

// ResolveViaSupersession resolves unresolved mapping edges whose cited
// to_version_label is superseded by an ingested version, using the explicit
// silver.version_relation(supersedes) chain. The edge keeps its original
// to_version_label (fidelity to source); provenance_detail records the
// version hop.
//
// Only single-hop supersession is followed (v8 → v8.1, v4.0 → v4.1). This
// is intentional: multi-hop chains risk silent mis-resolution across major
// version boundaries.
//
// Returns the number of edges resolved.
func ResolveViaSupersession(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (int64, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	const q = `
UPDATE silver.control_mapping cm
SET to_control_id = c.id,
    provenance_detail = cm.provenance_detail ||
        CASE WHEN cm.provenance_detail = '' THEN '' ELSE '; ' END ||
        'version-supersession: cited ' || cm.to_version_label ||
        ' resolved in successor ' || vr.from_version_label
FROM silver.version_relation vr
JOIN silver.document d ON d.framework_code = vr.from_framework_code
                      AND d.version_label  = vr.from_version_label
JOIN silver.control c  ON c.document_id    = d.id
WHERE cm.to_control_id IS NULL
  AND cm.to_version_label IS NOT NULL
  AND vr.to_framework_code   = cm.to_framework_code
  AND vr.to_version_label    = cm.to_version_label
  AND vr.relation_type       = 'supersedes'
  AND c.citation_norm        = cm.to_citation_norm`

	tag, err := pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("resolve via supersession: %w", err)
	}
	n := tag.RowsAffected()
	log.Info("resolve: cross-version supersession", "resolved", n)
	return n, nil
}

// EmitVersionSupersessions upserts the known version supersession relations
// into silver.version_relation. These are factual relations documented by
// the respective publishers. The mapedges stage calls this before the
// supersession-aware resolution pass.
func EmitVersionSupersessions(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	type rel struct {
		from, fromVer, to, toVer, note string
	}

	// Factual supersession relations:
	// - CIS Controls v8.1 supersedes v8 (documented in CIS v8.1 change-log)
	// - CSA CCM v4.1 supersedes v4.0 (documented by CSA)
	rels := []rel{
		{
			from: "ciscontrols", fromVer: "v8.1",
			to: "ciscontrols", toVer: "v8",
			note: "CIS Controls v8.1 supersedes v8 (CIS change-log workbook)",
		},
		{
			from: "csaccm", fromVer: "v4.1",
			to: "csaccm", toVer: "v4.0",
			note: "CSA CCM v4.1 supersedes v4.0 (CSA-documented)",
		},
	}

	const q = `
INSERT INTO silver.version_relation
    (from_framework_code, from_version_label, to_framework_code, to_version_label, relation_type, note)
VALUES ($1, $2, $3, $4, 'supersedes', $5)
ON CONFLICT (from_framework_code, from_version_label, to_framework_code, to_version_label, relation_type)
DO UPDATE SET note = EXCLUDED.note`

	for _, r := range rels {
		if _, err := pool.Exec(ctx, q, r.from, r.fromVer, r.to, r.toVer, r.note); err != nil {
			return fmt.Errorf("upsert version relation %s %s → %s %s: %w", r.from, r.fromVer, r.to, r.toVer, err)
		}
		log.Info("version relation upserted",
			"from", r.from, "from_version", r.fromVer,
			"to", r.to, "to_version", r.toVer)
	}
	return nil
}
