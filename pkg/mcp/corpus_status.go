package mcp

import (
	"context"
	"fmt"
)

// CorpusStatusOutput is the corpus_status tool's structured result.
type CorpusStatusOutput struct {
	SearchReady bool                     `json:"search_ready"`
	Frameworks  []FrameworkVersionStatus `json:"frameworks"`
	Totals      CorpusTotals             `json:"totals"`
	Notes       []string                 `json:"notes,omitempty"`
}

// FrameworkVersionStatus is per-framework/version counts.
type FrameworkVersionStatus struct {
	FrameworkCode string `json:"framework_code"`
	FrameworkName string `json:"framework_name"`
	VersionLabel  string `json:"version_label"`
	IsCurrent     bool   `json:"is_current"`
	ServePolicy   string `json:"serve_policy"`
	Documents     int64  `json:"documents"`
	Controls      int64  `json:"controls"`
	Withdrawn     int64  `json:"withdrawn"`
	Chunks        int64  `json:"chunks"`
	Embeddings    int64  `json:"embeddings"`
	MappingEdges  int64  `json:"mapping_edges"`
	Resolved      int64  `json:"resolved"`
	Unresolved    int64  `json:"unresolved"`
	InboundEdges  int64  `json:"inbound_edges"`
}

// CorpusTotals are aggregate counts across all frameworks.
type CorpusTotals struct {
	Frameworks   int64 `json:"frameworks"`
	Versions     int64 `json:"versions"`
	Documents    int64 `json:"documents"`
	Controls     int64 `json:"controls"`
	Withdrawn    int64 `json:"withdrawn"`
	Chunks       int64 `json:"chunks"`
	Embeddings   int64 `json:"embeddings"`
	MappingEdges int64 `json:"mapping_edges"`
	Resolved     int64 `json:"resolved"`
	Unresolved   int64 `json:"unresolved"`
	InboundEdges int64 `json:"inbound_edges"`
}

// CorpusStatus returns live per-framework/version counts.
func (c *Core) CorpusStatus(ctx context.Context) (CorpusStatusOutput, error) {
	if c.corpus == nil {
		return CorpusStatusOutput{}, errCorpusNotConfigured()
	}
	return c.corpus.CorpusStatus(ctx)
}

func (dc *dbCorpus) CorpusStatus(ctx context.Context) (CorpusStatusOutput, error) {
	const q = `
SELECT
    f.code AS framework_code,
    f.name AS framework_name,
    fv.version_label,
    fv.is_current,
    f.serve_policy,
    COALESCE(doc_counts.docs, 0) AS documents,
    COALESCE(ctrl_counts.controls, 0) AS controls,
    COALESCE(ctrl_counts.withdrawn, 0) AS withdrawn,
    COALESCE(chunk_counts.chunks, 0) AS chunks,
    COALESCE(embed_counts.embeddings, 0) AS embeddings,
    COALESCE(map_counts.total_edges, 0) AS mapping_edges,
    COALESCE(map_counts.resolved, 0) AS resolved,
    COALESCE(map_counts.unresolved, 0) AS unresolved,
    COALESCE(inbound_counts.inbound_edges, 0) AS inbound_edges
FROM config.framework f
JOIN config.framework_version fv
  ON fv.framework_code = f.code
LEFT JOIN LATERAL (
    SELECT count(*) AS docs
    FROM silver.document d
    WHERE d.framework_code = f.code AND d.version_label = fv.version_label
) doc_counts ON true
LEFT JOIN LATERAL (
    SELECT
        count(*) AS controls,
        count(*) FILTER (WHERE sc.status = 'withdrawn') AS withdrawn
    FROM silver.control sc
    JOIN silver.document d ON d.id = sc.document_id
    WHERE d.framework_code = f.code AND d.version_label = fv.version_label
) ctrl_counts ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS chunks
    FROM gold.chunk c
    JOIN silver.control sc ON sc.id = c.control_id
    JOIN silver.document d ON d.id = sc.document_id
    WHERE d.framework_code = f.code AND d.version_label = fv.version_label
) chunk_counts ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS embeddings
    FROM gold.chunk_embedding ce
    JOIN gold.chunk c ON c.id = ce.chunk_id
    JOIN silver.control sc ON sc.id = c.control_id
    JOIN silver.document d ON d.id = sc.document_id
    WHERE d.framework_code = f.code AND d.version_label = fv.version_label
) embed_counts ON true
LEFT JOIN LATERAL (
    SELECT
        count(*) AS total_edges,
        count(*) FILTER (WHERE cm.to_control_id IS NOT NULL) AS resolved,
        count(*) FILTER (WHERE cm.to_control_id IS NULL) AS unresolved
    FROM silver.control_mapping cm
    JOIN silver.control sc ON sc.id = cm.from_control_id
    JOIN silver.document d ON d.id = sc.document_id
    WHERE d.framework_code = f.code AND d.version_label = fv.version_label
) map_counts ON true
LEFT JOIN LATERAL (
    -- mapping_edges counts edges FROM this version; a framework rich in
    -- inbound mappings (e.g. ISO 27002) would otherwise read as unmapped.
    SELECT count(*) AS inbound_edges
    FROM silver.control_mapping cm
    JOIN silver.control tc ON tc.id = cm.to_control_id
    JOIN silver.document td ON td.id = tc.document_id
    WHERE td.framework_code = f.code AND td.version_label = fv.version_label
) inbound_counts ON true
WHERE COALESCE(doc_counts.docs, 0) > 0
   OR COALESCE(ctrl_counts.controls, 0) > 0
ORDER BY f.code, fv.version_label`

	rows, err := dc.pool.Query(ctx, q)
	if err != nil {
		return CorpusStatusOutput{}, fmt.Errorf("query corpus status: %w", err)
	}
	defer rows.Close()

	var frameworks []FrameworkVersionStatus
	var totals CorpusTotals
	fwSeen := make(map[string]bool)

	for rows.Next() {
		var fvs FrameworkVersionStatus
		if err := rows.Scan(
			&fvs.FrameworkCode,
			&fvs.FrameworkName,
			&fvs.VersionLabel,
			&fvs.IsCurrent,
			&fvs.ServePolicy,
			&fvs.Documents,
			&fvs.Controls,
			&fvs.Withdrawn,
			&fvs.Chunks,
			&fvs.Embeddings,
			&fvs.MappingEdges,
			&fvs.Resolved,
			&fvs.Unresolved,
			&fvs.InboundEdges,
		); err != nil {
			return CorpusStatusOutput{}, fmt.Errorf("scan corpus status: %w", err)
		}
		frameworks = append(frameworks, fvs)

		if !fwSeen[fvs.FrameworkCode] {
			fwSeen[fvs.FrameworkCode] = true
			totals.Frameworks++
		}
		totals.Versions++
		totals.Documents += fvs.Documents
		totals.Controls += fvs.Controls
		totals.Withdrawn += fvs.Withdrawn
		totals.Chunks += fvs.Chunks
		totals.Embeddings += fvs.Embeddings
		totals.MappingEdges += fvs.MappingEdges
		totals.Resolved += fvs.Resolved
		totals.Unresolved += fvs.Unresolved
		totals.InboundEdges += fvs.InboundEdges
	}
	if err := rows.Err(); err != nil {
		return CorpusStatusOutput{}, fmt.Errorf("corpus status rows: %w", err)
	}

	out := CorpusStatusOutput{
		SearchReady: totals.Chunks > 0,
		Frameworks:  frameworks,
		Totals:      totals,
		Notes:       corpusStatusNotes(totals),
	}
	return out, nil
}

func corpusStatusNotes(t CorpusTotals) []string {
	var notes []string
	if t.Chunks > 0 && t.Embeddings == 0 {
		notes = append(notes, "no dense embeddings stored — search runs BM25-only until the index stage embeds the corpus.")
	}
	if t.Chunks == 0 {
		notes = append(notes, "gold.chunk is empty; run the pipeline (manifest/extract/normalize/index) before using search.")
	}
	if t.Unresolved > 0 {
		notes = append(notes, fmt.Sprintf("%d mapping edges are unresolved — the target framework/version may not be ingested yet.", t.Unresolved))
	}
	if t.Withdrawn > 0 {
		notes = append(notes, fmt.Sprintf("%d controls are withdrawn; they are excluded from search by default (use include_withdrawn to retrieve them).", t.Withdrawn))
	}
	return notes
}
