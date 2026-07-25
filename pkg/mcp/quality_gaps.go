package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// QualityGapsInput is the quality_gaps tool's argument schema.
type QualityGapsInput struct {
	Category string `json:"category,omitempty" jsonschema:"one gap category: unresolved_mappings, curated_mappings, stale_mapedges, deferred_docs, manifest, body_quality, eval_floors; omit (or 'all') for every category"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max rows per category (default 20, max 100)"`
}

// QualityGapsOutput is the quality_gaps tool's structured result.
type QualityGapsOutput struct {
	Limit              int                    `json:"limit"`
	Categories         []string               `json:"categories"`
	UnresolvedMappings []UnresolvedMappingGap `json:"unresolved_mappings,omitempty"`
	CuratedMappingGaps []CuratedMappingGap    `json:"curated_mapping_gaps,omitempty"`
	StaleMapEdges      []StaleMapEdgesGap     `json:"stale_mapedges,omitempty"`
	DeferredDocs       []DeferredDocGap       `json:"deferred_docs,omitempty"`
	ManifestGaps       []ManifestGap          `json:"manifest_gaps,omitempty"`
	BodyQualityCaveats []BodyQualityCaveat    `json:"body_quality_caveats,omitempty"`
	EvalFloors         []EvalFloor            `json:"eval_floors,omitempty"`
	Notes              []string               `json:"notes,omitempty"`
}

// UnresolvedMappingGap is an unresolved mapping edge group.
type UnresolvedMappingGap struct {
	ToFrameworkCode string `json:"to_framework_code"`
	ToVersionLabel  string `json:"to_version_label,omitempty"`
	ToCitationNorm  string `json:"to_citation_norm"`
	MappingSource   string `json:"mapping_source_code"`
	Count           int64  `json:"count"`
	FromFrameworks  string `json:"from_frameworks,omitempty"`
}

// CuratedMappingGap is a seeded config.curated_mapping row that is not
// materialized as a silver.control_mapping edge.
type CuratedMappingGap struct {
	FromFrameworkCode string `json:"from_framework_code"`
	FromVersionLabel  string `json:"from_version_label"`
	FromCitationNorm  string `json:"from_citation_norm"`
	ToFrameworkCode   string `json:"to_framework_code"`
	ToVersionLabel    string `json:"to_version_label,omitempty"`
	ToCitationNorm    string `json:"to_citation_norm"`
	MappingSource     string `json:"mapping_source_code"`
	Reason            string `json:"reason"`
}

// StaleMapEdgesGap is a silver.document whose outbound mapping edges are not
// known to be current — silver.document.mapedges_at is NULL, either because the
// mapedges stage has never run over it or because a re-normalize cascade-deleted
// its edges since the last run.
type StaleMapEdgesGap struct {
	FrameworkCode string `json:"framework_code"`
	VersionLabel  string `json:"version_label"`
	DocRole       string `json:"doc_role"`
	Qualifier     string `json:"qualifier,omitempty"`
	Controls      int64  `json:"controls"`
	OutboundEdges int64  `json:"outbound_edges"`
	Reason        string `json:"reason"`
}

// DeferredDocGap is a document deferred from parsing.
type DeferredDocGap struct {
	FrameworkCode string `json:"framework_code"`
	VersionLabel  string `json:"version_label"`
	DocRole       string `json:"doc_role"`
	Qualifier     string `json:"qualifier,omitempty"`
	Reason        string `json:"reason"`
}

// ManifestGap is an unrecognized or errored manifest row.
type ManifestGap struct {
	RelPath string `json:"rel_path"`
	SHA256  string `json:"sha256,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// BodyQualityCaveat is a known body-quality issue.
type BodyQualityCaveat struct {
	Framework   string `json:"framework"`
	Description string `json:"description"`
}

// EvalFloor is a retrieval eval floor.
type EvalFloor struct {
	Metric string  `json:"metric"`
	Floor  float64 `json:"floor"`
	Last   float64 `json:"last_measured"`
}

const (
	gapCategoryAll         = "all"
	gapCategoryUnresolved  = "unresolved_mappings"
	gapCategoryCurated     = "curated_mappings"
	gapCategoryStaleEdges  = "stale_mapedges"
	gapCategoryDeferred    = "deferred_docs"
	gapCategoryManifest    = "manifest"
	gapCategoryBodyQuality = "body_quality"
	gapCategoryEvalFloors  = "eval_floors"

	defaultGapLimit = 20
	maxGapLimit     = 100
)

// QualityGaps returns evidence about what is missing from the corpus.
func (c *Core) QualityGaps(ctx context.Context, in QualityGapsInput) (QualityGapsOutput, error) {
	if c.corpus == nil {
		return QualityGapsOutput{}, errCorpusNotConfigured()
	}
	return c.corpus.QualityGaps(ctx, in)
}

func (dc *dbCorpus) QualityGaps(ctx context.Context, in QualityGapsInput) (QualityGapsOutput, error) {
	limit := normalizeLimit(in.Limit, defaultGapLimit, maxGapLimit)
	categories, err := gapCategories(in.Category)
	if err != nil {
		return QualityGapsOutput{}, err
	}

	out := QualityGapsOutput{
		Limit:      limit,
		Categories: categories,
		Notes: []string{
			"Rows are database worklists; fix the source/parser/mapping, rerun the pipeline, then re-check.",
		},
	}

	if gapIncludes(categories, gapCategoryUnresolved) {
		out.UnresolvedMappings, err = dc.unresolvedMappings(ctx, limit)
		if err != nil {
			return QualityGapsOutput{}, err
		}
	}

	if gapIncludes(categories, gapCategoryCurated) {
		gaps, total, err := dc.curatedMappingGaps(ctx, limit)
		if err != nil {
			return QualityGapsOutput{}, err
		}
		out.CuratedMappingGaps = gaps
		if total > 0 {
			out.Notes = append(out.Notes, curatedDriftNote(total))
		}
	}

	if gapIncludes(categories, gapCategoryStaleEdges) {
		gaps, total, err := dc.staleMapEdges(ctx, limit)
		if err != nil {
			return QualityGapsOutput{}, err
		}
		out.StaleMapEdges = gaps
		if total > 0 {
			out.Notes = append(out.Notes, staleMapEdgesNote(total))
		}
	}

	if gapIncludes(categories, gapCategoryDeferred) {
		out.DeferredDocs, err = dc.deferredDocs(ctx, limit)
		if err != nil {
			return QualityGapsOutput{}, err
		}
	}

	if gapIncludes(categories, gapCategoryManifest) {
		out.ManifestGaps, err = dc.manifestGaps(ctx, limit)
		if err != nil {
			return QualityGapsOutput{}, err
		}
	}

	if gapIncludes(categories, gapCategoryBodyQuality) {
		out.BodyQualityCaveats = staticBodyQualityCaveats()
	}

	if gapIncludes(categories, gapCategoryEvalFloors) {
		out.EvalFloors = staticEvalFloors()
	}

	return out, nil
}

func gapCategories(category string) ([]string, error) {
	category = strings.TrimSpace(strings.ToLower(category))
	if category == "" || category == gapCategoryAll {
		return []string{
			gapCategoryUnresolved,
			gapCategoryCurated,
			gapCategoryStaleEdges,
			gapCategoryDeferred,
			gapCategoryManifest,
			gapCategoryBodyQuality,
			gapCategoryEvalFloors,
		}, nil
	}
	switch category {
	case gapCategoryUnresolved,
		gapCategoryCurated,
		gapCategoryStaleEdges,
		gapCategoryDeferred,
		gapCategoryManifest,
		gapCategoryBodyQuality,
		gapCategoryEvalFloors:
		return []string{category}, nil
	default:
		return nil, fmt.Errorf("unknown quality gap category %q; valid: %s, %s, %s, %s, %s, %s, %s, or all",
			category, gapCategoryUnresolved, gapCategoryCurated, gapCategoryStaleEdges, gapCategoryDeferred, gapCategoryManifest, gapCategoryBodyQuality, gapCategoryEvalFloors)
	}
}

func gapIncludes(categories []string, category string) bool {
	return slices.Contains(categories, category)
}

// --- DB queries ---

func (dc *dbCorpus) unresolvedMappings(ctx context.Context, limit int) ([]UnresolvedMappingGap, error) {
	const q = `
SELECT
    cm.to_framework_code,
    COALESCE(cm.to_version_label, ''),
    cm.to_citation_norm,
    cm.mapping_source_code,
    count(*) AS cnt,
    string_agg(DISTINCT d.framework_code, ', ' ORDER BY d.framework_code) AS from_frameworks
FROM silver.control_mapping cm
JOIN silver.control sc ON sc.id = cm.from_control_id
JOIN silver.document d ON d.id = sc.document_id
WHERE cm.to_control_id IS NULL
GROUP BY cm.to_framework_code, cm.to_version_label, cm.to_citation_norm, cm.mapping_source_code
ORDER BY cnt DESC, cm.to_framework_code, cm.to_citation_norm
LIMIT $1`

	rows, err := dc.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("query unresolved mappings: %w", err)
	}
	defer rows.Close()

	var out []UnresolvedMappingGap
	for rows.Next() {
		var row UnresolvedMappingGap
		if err := rows.Scan(
			&row.ToFrameworkCode,
			&row.ToVersionLabel,
			&row.ToCitationNorm,
			&row.MappingSource,
			&row.Count,
			&row.FromFrameworks,
		); err != nil {
			return nil, fmt.Errorf("scan unresolved mapping: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// curatedMappingDriftQuery lists seeded config.curated_mapping rows that have
// no matching silver.control_mapping edge, newest-drift-agnostic and ordered
// deterministically. The match mirrors the mapedges upsert key
// (from_control_id, to_framework_code, to_version_label, to_citation_norm,
// mapping_source_code) and the case-insensitive from-side business key the
// curated pass uses. count(*) OVER () carries the pre-LIMIT total so the note
// can state the real drift size.
//
// Two distinct reasons are reported: the from-control exists but the edge is
// missing (stale mapedges — a normalize run cascade-deletes that framework's
// edges), versus the from-control is absent entirely (framework not ingested).
const curatedMappingDriftQuery = `
WITH missing AS (
    SELECT
        cur.from_framework_code,
        cur.from_version_label,
        cur.from_citation_norm,
        cur.to_framework_code,
        COALESCE(cur.to_version_label, '') AS to_version_label,
        cur.to_citation_norm,
        cur.mapping_source_code,
        EXISTS (
            SELECT 1
            FROM silver.control sc
            JOIN silver.document d ON d.id = sc.document_id
            WHERE d.framework_code = cur.from_framework_code
              AND d.version_label = cur.from_version_label
              AND upper(sc.citation_norm) = upper(cur.from_citation_norm)
        ) AS from_present
    FROM config.curated_mapping cur
    WHERE NOT EXISTS (
        SELECT 1
        FROM silver.control_mapping cm
        JOIN silver.control sc ON sc.id = cm.from_control_id
        JOIN silver.document d ON d.id = sc.document_id
        WHERE d.framework_code = cur.from_framework_code
          AND d.version_label = cur.from_version_label
          AND upper(sc.citation_norm) = upper(cur.from_citation_norm)
          AND cm.to_framework_code = cur.to_framework_code
          AND cm.to_citation_norm = cur.to_citation_norm
          AND cm.mapping_source_code = cur.mapping_source_code
          AND cm.to_version_label IS NOT DISTINCT FROM cur.to_version_label
    )
)
SELECT
    from_framework_code,
    from_version_label,
    from_citation_norm,
    to_framework_code,
    to_version_label,
    to_citation_norm,
    mapping_source_code,
    CASE WHEN from_present
        THEN 'seeded edge not materialized — re-run the mapedges pipeline stage'
        ELSE 'from-control not in corpus — that framework/version is not ingested'
    END AS reason,
    count(*) OVER () AS total
FROM missing
ORDER BY from_framework_code, from_version_label, from_citation_norm, to_framework_code, to_citation_norm
LIMIT $1`

// curatedDriftNote is the operator-facing remediation line attached to the
// notes list whenever curated-mapping drift is detected.
func curatedDriftNote(total int64) string {
	return fmt.Sprintf("curated mapping drift: %d seeded config.curated_mapping row(s) are not materialized in silver.control_mapping — run `go run ./cmd/pipeline -stage mapedges` (a normalize run cascade-deletes the re-written framework's mapping edges).", total)
}

// curatedMappingGaps returns the drift rows (capped by limit) and the pre-LIMIT
// total.
func (dc *dbCorpus) curatedMappingGaps(ctx context.Context, limit int) ([]CuratedMappingGap, int64, error) {
	rows, err := dc.pool.Query(ctx, curatedMappingDriftQuery, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query curated mapping gaps: %w", err)
	}
	defer rows.Close()

	var out []CuratedMappingGap
	var total int64
	for rows.Next() {
		var row CuratedMappingGap
		if err := rows.Scan(
			&row.FromFrameworkCode,
			&row.FromVersionLabel,
			&row.FromCitationNorm,
			&row.ToFrameworkCode,
			&row.ToVersionLabel,
			&row.ToCitationNorm,
			&row.MappingSource,
			&row.Reason,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan curated mapping gap: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// staleMapEdgesQuery lists silver.document rows whose mapedges marker is NULL —
// the documents whose outbound mapping edges are not known to be current.
//
// This is the derived-edge counterpart of curatedMappingDriftQuery: that one
// detects drift only for edges seeded in config.curated_mapping (39 of 4,501),
// by diffing seed against corpus. Derived edges (iso-structural, nist-olir,
// cis-v8.1-mappings) have no seed table to diff against, so the only way to know
// they are stale is the marker the normalize cascade clears.
//
// Two reasons, distinguished by whether the stage has demonstrably run: a corpus
// with no fresh marker AND no edges at all has simply never run the stage (a
// legitimate state for a fresh self-deploy), whereas a NULL alongside fresh
// siblings — or alongside existing edges, which a full-corpus re-normalize
// leaves behind — is a regression.
// outbound_edges is the count that survived (parser-carried edges are re-emitted
// by normalize itself), so the operator can see the residual exposure. A document
// with zero outbound edges can still be stale-worthy: the inbound FK is
// ON DELETE SET NULL, so re-normalizing it un-resolves every edge pointing INTO
// it (those surface under unresolved_mappings until mapedges re-resolves them).
const staleMapEdgesQuery = `
SELECT
    d.framework_code,
    d.version_label,
    d.doc_role,
    d.qualifier,
    (SELECT count(*) FROM silver.control sc WHERE sc.document_id = d.id) AS controls,
    (SELECT count(*) FROM silver.control_mapping cm
       JOIN silver.control sc ON sc.id = cm.from_control_id
      WHERE sc.document_id = d.id) AS outbound_edges,
    CASE WHEN EXISTS (SELECT 1 FROM silver.document f WHERE f.mapedges_at IS NOT NULL)
          OR EXISTS (SELECT 1 FROM silver.control_mapping)
        THEN 'document re-normalized since the last mapedges run — its outbound mapping edges were cascade-deleted, and inbound edges pointing into it were un-resolved'
        ELSE 'mapedges stage has never run over this corpus — no derived or curated edges are guaranteed'
    END AS reason,
    count(*) OVER () AS total
FROM silver.document d
WHERE d.mapedges_at IS NULL
ORDER BY d.framework_code, d.version_label, d.doc_role, d.qualifier
LIMIT $1`

// staleMapEdgesNote is the operator-facing remediation line attached to the
// notes list whenever a stale mapedges marker is detected.
func staleMapEdgesNote(total int64) string {
	return fmt.Sprintf("stale mapping edges: %d document(s) have silver.document.mapedges_at NULL — their outbound cross-framework edges are unbuilt or were cascade-deleted by a re-normalize. Run `go run ./cmd/pipeline -stage mapedges` (it is not in the default stage list).", total)
}

// staleMapEdges returns the stale-marker rows (capped by limit) and the
// pre-LIMIT total.
func (dc *dbCorpus) staleMapEdges(ctx context.Context, limit int) ([]StaleMapEdgesGap, int64, error) {
	rows, err := dc.pool.Query(ctx, staleMapEdgesQuery, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query stale mapedges: %w", err)
	}
	defer rows.Close()

	var out []StaleMapEdgesGap
	var total int64
	for rows.Next() {
		var row StaleMapEdgesGap
		if err := rows.Scan(
			&row.FrameworkCode,
			&row.VersionLabel,
			&row.DocRole,
			&row.Qualifier,
			&row.Controls,
			&row.OutboundEdges,
			&row.Reason,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan stale mapedges: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (dc *dbCorpus) deferredDocs(ctx context.Context, limit int) ([]DeferredDocGap, error) {
	// Deferred documents: manifest entries that match a file_rule but have no
	// corresponding silver.document (either because the role is non-extractable
	// or the parser hasn't been built yet).
	const q = `
SELECT
    mf.framework_code,
    mf.version_label,
    mf.doc_role,
    COALESCE(mf.qualifier, ''),
    CASE
        WHEN mf.doc_role = 'amendment' THEN 'amendment parsing deferred'
        WHEN mf.doc_role = 'companion-workbook' THEN 'companion-workbook parsing deferred (e.g. CAIQ)'
        ELSE 'parsing not yet implemented'
    END AS reason
FROM ingest.manifest_file mf
WHERE mf.framework_code IS NOT NULL
  AND NOT mf.ignored
  AND mf.doc_role NOT IN ('guide', 'changelog')
  AND NOT EXISTS (
      SELECT 1 FROM silver.document d
      WHERE d.framework_code = mf.framework_code
        AND d.version_label = mf.version_label
        AND d.doc_role = mf.doc_role
        AND d.qualifier = COALESCE(mf.qualifier, '')
  )
ORDER BY mf.framework_code, mf.version_label, mf.doc_role
LIMIT $1`

	rows, err := dc.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("query deferred docs: %w", err)
	}
	defer rows.Close()

	var out []DeferredDocGap
	for rows.Next() {
		var row DeferredDocGap
		if err := rows.Scan(
			&row.FrameworkCode,
			&row.VersionLabel,
			&row.DocRole,
			&row.Qualifier,
			&row.Reason,
		); err != nil {
			return nil, fmt.Errorf("scan deferred doc: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (dc *dbCorpus) manifestGaps(ctx context.Context, limit int) ([]ManifestGap, error) {
	const q = `
SELECT
    mf.rel_path,
    COALESCE(mf.sha256, ''),
    CASE
        WHEN mf.framework_code IS NULL AND NOT mf.ignored THEN 'unrecognized'
        WHEN mf.stage_error IS NOT NULL AND mf.stage_error <> '' THEN 'error'
        ELSE 'ok'
    END AS status,
    COALESCE(mf.stage_error, '')
FROM ingest.manifest_file mf
WHERE (mf.framework_code IS NULL AND NOT mf.ignored)
   OR (mf.stage_error IS NOT NULL AND mf.stage_error <> '')
ORDER BY mf.rel_path
LIMIT $1`

	rows, err := dc.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("query manifest gaps: %w", err)
	}
	defer rows.Close()

	var out []ManifestGap
	for rows.Next() {
		var row ManifestGap
		if err := rows.Scan(&row.RelPath, &row.SHA256, &row.Status, &row.Error); err != nil {
			return nil, fmt.Errorf("scan manifest gap: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// --- Static caveats ---

func staticBodyQualityCaveats() []BodyQualityCaveat {
	return []BodyQualityCaveat{
		{
			Framework:   "pcidss",
			Description: "PCI DSS v4.0.1: the normalizer truncates requirement bodies at the column boundary (Testing Procedures / Guidance headers) to exclude non-requirement text. A small number of requirements may retain trailing noise where go-fitz column concatenation hides the boundary header.",
		},
		{
			Framework:   "iso27001",
			Description: "ISO 27001:2022 Annex A: control bodies from the PDF table are shallow (one-line statements extracted from the table row); full implementation guidance lives in ISO 27002:2022. For retrieval, each Annex A chunk is enriched with its 27002 equivalent's guidance (labeled with the 27002 citation), so topic searches find the 27001 control; the served body remains the 27001 statement only.",
		},
		{
			Framework:   "soc2tsc",
			Description: "AICPA TSC 2017: the publisher's terms explicitly object to LLM/AI knowledge-base inclusion. Ingestion is enabled with an operator-visible terms_note warning; serve_gate is auth-only.",
		},
	}
}

// EvalFloors exposes the accepted eval floors and last-measured values — the
// same payload quality_gaps serves — so other surfaces (the landing page)
// render these numbers from one source instead of hardcoding copies.
func EvalFloors() []EvalFloor { return staticEvalFloors() }

func staticEvalFloors() []EvalFloor {
	// Open-corpus lane on the 205-case golden set (v6), re-baselined
	// 2026-07-25 after the 20 Vietnamese TCVN cases landed. The v5→v6 lift is
	// set composition (the new TCVN cases score above the corpus average), not
	// a retriever change — a control run of the unchanged 185-case v5 set
	// reproduced v5 exactly. Abstention uses the raw-cosine floor
	// (search_abstain_floor = 0.5): it catches clearly-distant OOS queries, but
	// compliance-adjacent OOS still embeds close to InfoSec text.
	// Keep in sync with docs/design/MCP.md and docs/design/RETRIEVAL.md —
	// TestStaticEvalFloors_V6Baseline pins these values.
	return []EvalFloor{
		{Metric: "recall@8", Floor: 0.66, Last: 0.863},
		{Metric: "MRR@8", Floor: 0.44, Last: 0.663},
		{Metric: "current-version", Floor: 0.98, Last: 1.000},
		{Metric: "abstention", Floor: 0.90, Last: 0.966},
	}
}
