package mcp

import (
	"context"
	"fmt"
	"strings"
)

// QualityGapsInput is the quality_gaps tool's argument schema.
type QualityGapsInput struct {
	Category string `json:"category,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// QualityGapsOutput is the quality_gaps tool's structured result.
type QualityGapsOutput struct {
	Limit              int                    `json:"limit"`
	Categories         []string               `json:"categories"`
	UnresolvedMappings []UnresolvedMappingGap `json:"unresolved_mappings,omitempty"`
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
			gapCategoryDeferred,
			gapCategoryManifest,
			gapCategoryBodyQuality,
			gapCategoryEvalFloors,
		}, nil
	}
	switch category {
	case gapCategoryUnresolved,
		gapCategoryDeferred,
		gapCategoryManifest,
		gapCategoryBodyQuality,
		gapCategoryEvalFloors:
		return []string{category}, nil
	default:
		return nil, fmt.Errorf("unknown quality gap category %q", category)
	}
}

func gapIncludes(categories []string, category string) bool {
	for _, got := range categories {
		if got == category {
			return true
		}
	}
	return false
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
        WHEN mf.doc_role = 'changelog' THEN 'changelog — not parsed (metadata only)'
        WHEN mf.doc_role = 'guide' THEN 'guide — recorded, not parsed'
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
			Description: "ISO 27001:2022 Annex A: control bodies from the PDF table are shallow (one-line statements extracted from the table row). Full implementation guidance lives in ISO 27002:2022, which is a separate document.",
		},
		{
			Framework:   "soc2tsc",
			Description: "AICPA TSC 2017: the publisher's terms explicitly object to LLM/AI knowledge-base inclusion. Ingestion is enabled with an operator-visible terms_note warning; serve_gate is auth-only.",
		},
	}
}

func staticEvalFloors() []EvalFloor {
	return []EvalFloor{
		{Metric: "recall@8", Floor: 0.63, Last: 0.650},
		{Metric: "MRR@8", Floor: 0.41, Last: 0.446},
		{Metric: "current-version", Floor: 0.98, Last: 1.000},
		{Metric: "abstention", Floor: 0.93, Last: 0.952},
	}
}
