package mcp

import (
	"context"
	"fmt"
	"strings"
)

// DocumentInput is the document tool's argument schema.
type DocumentInput struct {
	Citation      string   `json:"citation,omitempty"`
	FrameworkCode string   `json:"framework_code,omitempty"`
	VersionLabel  string   `json:"version_label,omitempty"`
	Include       []string `json:"include,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Offset        int      `json:"offset,omitempty"`
}

// DocumentOutput is the document tool's structured result.
type DocumentOutput struct {
	Found           bool                `json:"found"`
	Control         *ControlDetail      `json:"control,omitempty"`
	Parent          *ControlBrief       `json:"parent,omitempty"`
	Children        []ControlBrief      `json:"children,omitempty"`
	Mappings        []MappingEdge       `json:"mappings,omitempty"`
	InboundMappings []MappingEdge       `json:"inbound_mappings,omitempty"`
	VersionLineage  []VersionLineageRow `json:"version_lineage,omitempty"`
	Chunks          []DocumentChunk     `json:"chunks,omitempty"`
	Gaps            []SearchGap         `json:"gaps,omitempty"`
	Limit           int                 `json:"limit"`
	Offset          int                 `json:"offset"`
	NextOffset      int                 `json:"next_offset,omitempty"`
}

// ControlDetail is the full control record.
type ControlDetail struct {
	ControlID     int64  `json:"control_id"`
	DocumentID    int64  `json:"document_id"`
	FrameworkCode string `json:"framework_code"`
	VersionLabel  string `json:"version_label"`
	Citation      string `json:"citation"`
	CitationNorm  string `json:"citation_norm"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Title         string `json:"title"`
	TitleOriginal string `json:"title_original,omitempty"`
	Body          string `json:"body,omitempty"`
	IsCurrent     bool   `json:"is_current"`
	VersionStatus string `json:"version_status"`
	ServeGate     string `json:"serve_gate"`
}

// ControlBrief is a summary for parent/child context.
type ControlBrief struct {
	ControlID    int64  `json:"control_id"`
	Citation     string `json:"citation"`
	CitationNorm string `json:"citation_norm"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Title        string `json:"title"`
}

// MappingEdge is one cross-framework mapping relationship.
type MappingEdge struct {
	Direction        string `json:"direction"`
	FrameworkCode    string `json:"framework_code"`
	VersionLabel     string `json:"version_label,omitempty"`
	CitationNorm     string `json:"citation_norm"`
	Resolved         bool   `json:"resolved"`
	ResolvedTitle    string `json:"resolved_title,omitempty"`
	Relationship     string `json:"relationship"`
	MappingSource    string `json:"mapping_source_code"`
	ProvenanceDetail string `json:"provenance_detail,omitempty"`
}

// VersionLineageRow is one version relation or currency row.
type VersionLineageRow struct {
	Kind          string `json:"kind"`
	FrameworkCode string `json:"framework_code"`
	VersionLabel  string `json:"version_label"`
	RelationType  string `json:"relation_type,omitempty"`
	IsCurrent     bool   `json:"is_current"`
	Note          string `json:"note,omitempty"`
}

// DocumentChunk is one chunk from the gold layer for this control.
type DocumentChunk struct {
	ChunkID       int64  `json:"chunk_id"`
	Citation      string `json:"citation"`
	ContextPrefix string `json:"context_prefix,omitempty"`
	Content       string `json:"content"`
	Ordinal       int32  `json:"ordinal"`
}

const (
	defaultDocChunkLimit = 20
	maxDocChunkLimit     = 50
)

// documentSections names the optional document response sections.
var documentSections = map[string]bool{
	"chunks":   true,
	"mappings": true,
	"lineage":  true,
	"children": true,
}

func documentIncludes(include []string) map[string]bool {
	if len(include) == 0 {
		return map[string]bool{"chunks": true, "mappings": true, "lineage": true, "children": true}
	}
	m := make(map[string]bool, len(include))
	for _, name := range include {
		if n := strings.ToLower(strings.TrimSpace(name)); documentSections[n] {
			m[n] = true
		}
	}
	return m
}

// Document returns one control by citation with mappings, lineage, and context.
func (c *Core) Document(ctx context.Context, in DocumentInput) (DocumentOutput, error) {
	if c.corpus == nil {
		return DocumentOutput{}, errCorpusNotConfigured()
	}
	citation := strings.TrimSpace(in.Citation)
	if citation == "" {
		return DocumentOutput{}, fmt.Errorf("citation is required")
	}
	out, err := c.corpus.Document(ctx, in)
	if err != nil {
		c.log.Error("mcp: document", "err", err)
		return DocumentOutput{}, fmt.Errorf("document: %w", err)
	}
	return c.ProjectDocument(out), nil
}

// --- DB implementation ---

func (dc *dbCorpus) Document(ctx context.Context, in DocumentInput) (DocumentOutput, error) {
	limit := normalizeLimit(in.Limit, defaultDocChunkLimit, maxDocChunkLimit)
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	citation := strings.TrimSpace(in.Citation)

	out := DocumentOutput{
		Limit:  limit,
		Offset: offset,
		Chunks: []DocumentChunk{},
	}

	// Look up the control by citation_norm, with optional framework/version filter.
	ctrl, found, err := dc.findControl(ctx, citation, in.FrameworkCode, in.VersionLabel)
	if err != nil {
		return DocumentOutput{}, err
	}
	if !found {
		out.Found = false
		out.Gaps = []SearchGap{{
			Kind:         "no_evidence",
			Message:      fmt.Sprintf("control not found by citation %q", citation),
			BlocksAnswer: true,
		}}
		return out, nil
	}
	out.Found = true
	out.Control = &ctrl

	inc := documentIncludes(in.Include)

	// Parent context.
	parent, err := dc.controlParent(ctx, ctrl.ControlID)
	if err != nil {
		return DocumentOutput{}, err
	}
	out.Parent = parent

	if inc["children"] {
		children, err := dc.controlChildren(ctx, ctrl.ControlID)
		if err != nil {
			return DocumentOutput{}, err
		}
		out.Children = children
	}

	if inc["mappings"] {
		outbound, err := dc.outboundMappings(ctx, ctrl.ControlID)
		if err != nil {
			return DocumentOutput{}, err
		}
		out.Mappings = outbound

		inbound, err := dc.inboundMappings(ctx, ctrl.ControlID)
		if err != nil {
			return DocumentOutput{}, err
		}
		out.InboundMappings = inbound
	}

	if inc["lineage"] {
		lineage, err := dc.versionLineage(ctx, ctrl.FrameworkCode, ctrl.VersionLabel)
		if err != nil {
			return DocumentOutput{}, err
		}
		out.VersionLineage = lineage
	}

	if inc["chunks"] {
		chunks, err := dc.controlChunks(ctx, ctrl.ControlID, limit, offset)
		if err != nil {
			return DocumentOutput{}, err
		}
		out.Chunks = chunks
		if len(chunks) == limit {
			out.NextOffset = offset + limit
		}
	}

	return out, nil
}

// --- DB helpers ---

func (dc *dbCorpus) findControl(ctx context.Context, citation, frameworkCode, versionLabel string) (ControlDetail, bool, error) {
	var conds []string
	var args []any
	p := 1

	// Exact match on citation_norm (case-insensitive). Also try replacing
	// bare single-digit family numbers with zero-padded (e.g. AC-2 -> AC-02)
	// to handle both agent input styles.
	conds = append(conds, fmt.Sprintf("(upper(sc.citation_norm) = upper($%d) OR upper(sc.citation_norm) = upper($%d))", p, p+1))
	args = append(args, citation, zeroPadCitation(citation))
	p += 2

	if frameworkCode != "" {
		conds = append(conds, fmt.Sprintf("d.framework_code = $%d", p))
		args = append(args, frameworkCode)
		p++
	}

	if versionLabel != "" {
		conds = append(conds, fmt.Sprintf("d.version_label = $%d", p))
		args = append(args, versionLabel)
		p++
	} else {
		// Default to current version.
		conds = append(conds, `fv.is_current = true`)
	}

	sql := fmt.Sprintf(`
SELECT
    sc.id,
    sc.document_id,
    d.framework_code,
    d.version_label,
    sc.citation,
    sc.citation_norm,
    sc.kind,
    sc.status,
    sc.title,
    COALESCE(sc.title_original, ''),
    COALESCE(sc.body, ''),
    fv.is_current,
    d.serve_gate
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code
 AND fv.version_label = d.version_label
WHERE %s
ORDER BY fv.is_current DESC, d.framework_code, sc.citation_norm
LIMIT 1`, strings.Join(conds, " AND "))

	var ctrl ControlDetail
	err := dc.pool.QueryRow(ctx, sql, args...).Scan(
		&ctrl.ControlID,
		&ctrl.DocumentID,
		&ctrl.FrameworkCode,
		&ctrl.VersionLabel,
		&ctrl.Citation,
		&ctrl.CitationNorm,
		&ctrl.Kind,
		&ctrl.Status,
		&ctrl.Title,
		&ctrl.TitleOriginal,
		&ctrl.Body,
		&ctrl.IsCurrent,
		&ctrl.ServeGate,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return ControlDetail{}, false, nil
		}
		return ControlDetail{}, false, fmt.Errorf("find control: %w", err)
	}
	ctrl.VersionStatus = versionStatus(ctrl.IsCurrent)
	return ctrl, true, nil
}

// zeroPadCitation pads bare single-digit numbers in a citation to two digits
// so "AC-2" matches "AC-02", "AC-2(3)" matches "AC-02(03)", etc. If the
// citation is already padded or doesn't contain a pattern to pad, returns
// unchanged.
func zeroPadCitation(citation string) string {
	// Simple regex-free approach: find the pattern XX-N and pad to XX-0N.
	// Also handle (N) -> (0N).
	result := make([]byte, 0, len(citation)+4)
	for i := 0; i < len(citation); i++ {
		b := citation[i]
		if b == '-' && i+1 < len(citation) && citation[i+1] >= '1' && citation[i+1] <= '9' {
			// Check if the next character after the digit is non-digit.
			if i+2 >= len(citation) || citation[i+2] < '0' || citation[i+2] > '9' {
				result = append(result, '-', '0')
				continue
			}
		}
		if b == '(' && i+1 < len(citation) && citation[i+1] >= '1' && citation[i+1] <= '9' {
			if i+2 < len(citation) && citation[i+2] == ')' {
				result = append(result, '(', '0')
				continue
			}
		}
		result = append(result, b)
	}
	return string(result)
}

func (dc *dbCorpus) controlParent(ctx context.Context, controlID int64) (*ControlBrief, error) {
	const q = `
SELECT p.id, p.citation, p.citation_norm, p.kind, p.status, p.title
FROM silver.control sc
JOIN silver.control p ON p.id = sc.parent_control_id
WHERE sc.id = $1 AND sc.parent_control_id IS NOT NULL`

	var parent ControlBrief
	err := dc.pool.QueryRow(ctx, q, controlID).Scan(
		&parent.ControlID,
		&parent.Citation,
		&parent.CitationNorm,
		&parent.Kind,
		&parent.Status,
		&parent.Title,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("control parent: %w", err)
	}
	return &parent, nil
}

func (dc *dbCorpus) controlChildren(ctx context.Context, controlID int64) ([]ControlBrief, error) {
	const q = `
SELECT id, citation, citation_norm, kind, status, title
FROM silver.control
WHERE parent_control_id = $1
ORDER BY ordinal, id`

	rows, err := dc.pool.Query(ctx, q, controlID)
	if err != nil {
		return nil, fmt.Errorf("query control children: %w", err)
	}
	defer rows.Close()

	var out []ControlBrief
	for rows.Next() {
		var cb ControlBrief
		if err := rows.Scan(&cb.ControlID, &cb.Citation, &cb.CitationNorm, &cb.Kind, &cb.Status, &cb.Title); err != nil {
			return nil, fmt.Errorf("scan control child: %w", err)
		}
		out = append(out, cb)
	}
	return out, rows.Err()
}

func (dc *dbCorpus) outboundMappings(ctx context.Context, controlID int64) ([]MappingEdge, error) {
	const q = `
SELECT
    cm.to_framework_code,
    COALESCE(cm.to_version_label, ''),
    cm.to_citation_norm,
    cm.to_control_id IS NOT NULL AS resolved,
    COALESCE(tc.title, ''),
    cm.relationship,
    cm.mapping_source_code,
    cm.provenance_detail
FROM silver.control_mapping cm
LEFT JOIN silver.control tc ON tc.id = cm.to_control_id
WHERE cm.from_control_id = $1
ORDER BY cm.to_framework_code, cm.to_citation_norm`

	rows, err := dc.pool.Query(ctx, q, controlID)
	if err != nil {
		return nil, fmt.Errorf("query outbound mappings: %w", err)
	}
	defer rows.Close()

	var out []MappingEdge
	for rows.Next() {
		var me MappingEdge
		me.Direction = "outbound"
		if err := rows.Scan(
			&me.FrameworkCode,
			&me.VersionLabel,
			&me.CitationNorm,
			&me.Resolved,
			&me.ResolvedTitle,
			&me.Relationship,
			&me.MappingSource,
			&me.ProvenanceDetail,
		); err != nil {
			return nil, fmt.Errorf("scan outbound mapping: %w", err)
		}
		out = append(out, me)
	}
	return out, rows.Err()
}

func (dc *dbCorpus) inboundMappings(ctx context.Context, controlID int64) ([]MappingEdge, error) {
	const q = `
SELECT
    d.framework_code,
    d.version_label,
    sc.citation_norm,
    true AS resolved,
    sc.title,
    cm.relationship,
    cm.mapping_source_code,
    cm.provenance_detail
FROM silver.control_mapping cm
JOIN silver.control sc ON sc.id = cm.from_control_id
JOIN silver.document d ON d.id = sc.document_id
WHERE cm.to_control_id = $1
ORDER BY d.framework_code, sc.citation_norm`

	rows, err := dc.pool.Query(ctx, q, controlID)
	if err != nil {
		return nil, fmt.Errorf("query inbound mappings: %w", err)
	}
	defer rows.Close()

	var out []MappingEdge
	for rows.Next() {
		var me MappingEdge
		me.Direction = "inbound"
		me.Resolved = true
		if err := rows.Scan(
			&me.FrameworkCode,
			&me.VersionLabel,
			&me.CitationNorm,
			&me.Resolved,
			&me.ResolvedTitle,
			&me.Relationship,
			&me.MappingSource,
			&me.ProvenanceDetail,
		); err != nil {
			return nil, fmt.Errorf("scan inbound mapping: %w", err)
		}
		out = append(out, me)
	}
	return out, rows.Err()
}

func (dc *dbCorpus) versionLineage(ctx context.Context, frameworkCode, versionLabel string) ([]VersionLineageRow, error) {
	// Version relations involving this framework version.
	const relQ = `
SELECT
    CASE WHEN vr.from_framework_code = $1 AND vr.from_version_label = $2
         THEN 'from' ELSE 'to' END AS kind,
    CASE WHEN vr.from_framework_code = $1 AND vr.from_version_label = $2
         THEN vr.to_framework_code ELSE vr.from_framework_code END,
    CASE WHEN vr.from_framework_code = $1 AND vr.from_version_label = $2
         THEN vr.to_version_label ELSE vr.from_version_label END,
    vr.relation_type,
    COALESCE(fv.is_current, false),
    vr.note
FROM silver.version_relation vr
LEFT JOIN config.framework_version fv
  ON fv.framework_code = CASE
       WHEN vr.from_framework_code = $1 AND vr.from_version_label = $2
       THEN vr.to_framework_code ELSE vr.from_framework_code END
 AND fv.version_label = CASE
       WHEN vr.from_framework_code = $1 AND vr.from_version_label = $2
       THEN vr.to_version_label ELSE vr.from_version_label END
WHERE (vr.from_framework_code = $1 AND vr.from_version_label = $2)
   OR (vr.to_framework_code = $1 AND vr.to_version_label = $2)
ORDER BY vr.relation_type, vr.from_version_label`

	rows, err := dc.pool.Query(ctx, relQ, frameworkCode, versionLabel)
	if err != nil {
		return nil, fmt.Errorf("query version lineage: %w", err)
	}
	defer rows.Close()

	var out []VersionLineageRow
	for rows.Next() {
		var row VersionLineageRow
		if err := rows.Scan(
			&row.Kind,
			&row.FrameworkCode,
			&row.VersionLabel,
			&row.RelationType,
			&row.IsCurrent,
			&row.Note,
		); err != nil {
			return nil, fmt.Errorf("scan version lineage: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("version lineage rows: %w", err)
	}

	// Also include all known versions for this framework with their currency.
	const versQ = `
SELECT framework_code, version_label, is_current, edition_note
FROM config.framework_version
WHERE framework_code = $1
ORDER BY version_label`

	vrows, err := dc.pool.Query(ctx, versQ, frameworkCode)
	if err != nil {
		return nil, fmt.Errorf("query framework versions: %w", err)
	}
	defer vrows.Close()
	for vrows.Next() {
		var row VersionLineageRow
		row.Kind = "version"
		if err := vrows.Scan(&row.FrameworkCode, &row.VersionLabel, &row.IsCurrent, &row.Note); err != nil {
			return nil, fmt.Errorf("scan framework version: %w", err)
		}
		out = append(out, row)
	}
	return out, vrows.Err()
}

func (dc *dbCorpus) controlChunks(ctx context.Context, controlID int64, limit, offset int) ([]DocumentChunk, error) {
	const q = `
SELECT c.id, c.citation, COALESCE(c.context_prefix, ''), c.content, c.ordinal
FROM gold.chunk c
WHERE c.control_id = $1
ORDER BY c.ordinal, c.id
LIMIT $2 OFFSET $3`

	rows, err := dc.pool.Query(ctx, q, controlID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query control chunks: %w", err)
	}
	defer rows.Close()

	var out []DocumentChunk
	for rows.Next() {
		var ch DocumentChunk
		if err := rows.Scan(&ch.ChunkID, &ch.Citation, &ch.ContextPrefix, &ch.Content, &ch.Ordinal); err != nil {
			return nil, fmt.Errorf("scan control chunk: %w", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}
