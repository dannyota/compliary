package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DocumentInput is the document tool's argument schema. Citation carries no
// omitempty: it is required at runtime, and the generated schema must say so.
type DocumentInput struct {
	Citation      string   `json:"citation" jsonschema:"control citation to open (e.g. AC-2(3), A.5.1, CC6.1, 8.3.6, PR.AA-01); required"`
	FrameworkCode string   `json:"framework_code,omitempty" jsonschema:"pin one framework code when the citation is ambiguous across frameworks (codes listed by corpus_status)"`
	VersionLabel  string   `json:"version_label,omitempty" jsonschema:"pin one framework version (e.g. r5, 2022); omit for the current version"`
	Include       []string `json:"include,omitempty" jsonschema:"response sections to return: chunks, mappings, lineage, children; omit for all four. include=[chunks] is the cheapest way to read one control's text"`
	Limit         int      `json:"limit,omitempty" jsonschema:"max chunks per page (default 20, max 50)"`
	Offset        int      `json:"offset,omitempty" jsonschema:"chunk pagination offset; use next_offset from the previous call"`
}

// DocumentOutput is the document tool's structured result.
type DocumentOutput struct {
	Found           bool                `json:"found"`
	Control         *ControlDetail      `json:"control,omitempty"`
	AmendedBy       []AmendmentRef      `json:"amended_by,omitempty"`
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

// AmendmentRef is one amendment patch applied to the looked-up control: a row
// from an amendment document of the same framework version whose
// amends_citation_norm targets this control. Title is a generated neutral
// label; Body carries the verbatim instruction text and is stripped under the
// reduced projection like every licensed field.
type AmendmentRef struct {
	Citation  string `json:"citation"`
	Action    string `json:"action"` // add | replace | delete
	Qualifier string `json:"qualifier,omitempty"`
	DocKey    string `json:"doc_key"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
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
	SourceURL     string `json:"source_url,omitempty"`
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

// validateIncludes rejects unrecognized include names. Silently ignoring a
// typo ("chunk" for "chunks") used to return a found-control with no sections
// and no explanation — the worst kind of empty answer.
func validateIncludes(include []string) error {
	for _, name := range include {
		if n := strings.ToLower(strings.TrimSpace(name)); !documentSections[n] {
			return fmt.Errorf("unknown include section %q; valid: chunks, mappings, lineage, children", name)
		}
	}
	return nil
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
	if err := validateIncludes(in.Include); err != nil {
		return DocumentOutput{}, err
	}
	in.Citation = normalizeCitationInput(citation)
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

	// Chunks stays nil unless the section was requested — with omitempty, a
	// "chunks" key must mean "requested", never "initialized". A requested
	// section with zero rows is reported via a no_chunks gap instead.
	out := DocumentOutput{
		Limit:  limit,
		Offset: offset,
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
		// If a version pin caused the miss, say so and name the versions that
		// DO carry the citation — the agent should never have to guess whether
		// the citation or the version was wrong.
		if in.VersionLabel != "" {
			if vers, err := dc.citationVersions(ctx, citation, in.FrameworkCode); err == nil && len(vers) > 0 {
				out.Gaps = append(out.Gaps, SearchGap{
					Kind:         "version_not_found",
					Message:      fmt.Sprintf("citation %q exists but not in version %q; found in: %s", citation, in.VersionLabel, strings.Join(vers, ", ")),
					BlocksAnswer: true,
				})
			}
		}
		// Same courtesy for a framework pin: name the other frameworks whose
		// current version carries the citation, so a wrong pin is diagnosable.
		if in.FrameworkCode != "" {
			if others, err := dc.citationFrameworks(ctx, citation, in.FrameworkCode); err == nil && len(others) > 0 {
				out.Gaps = append(out.Gaps, SearchGap{
					Kind:         "found_elsewhere",
					Message:      fmt.Sprintf("citation %q is not in framework %q but exists in: %s", citation, in.FrameworkCode, strings.Join(others, ", ")),
					BlocksAnswer: false,
				})
			}
		}
		return out, nil
	}
	out.Found = true
	out.Control = &ctrl

	// Ambiguity notice: bare numeric citations (5.1, 8.1) exist in several
	// frameworks. When the caller pinned no framework, report the alternatives
	// so the agent knows this pick was a ranking choice, not the only match.
	if in.FrameworkCode == "" {
		if others, err := dc.citationFrameworks(ctx, citation, ctrl.FrameworkCode); err == nil && len(others) > 0 {
			out.Gaps = append(out.Gaps, SearchGap{
				Kind:         "ambiguous_citation",
				Message:      fmt.Sprintf("citation %q also exists in: %s — pass framework_code to pin", citation, strings.Join(others, ", ")),
				BlocksAnswer: false,
			})
		}
	}

	inc := documentIncludes(in.Include)

	// Amendment patches targeting this control (always included — at most a
	// handful of rows, and an agent that misses a patch cites stale text).
	amendedBy, err := dc.controlAmendments(ctx, ctrl)
	if err != nil {
		return DocumentOutput{}, err
	}
	out.AmendedBy = amendedBy

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
		// The query probed limit+1 rows: an extra row proves a next page.
		if len(chunks) > limit {
			out.Chunks = chunks[:limit]
			out.NextOffset = offset + limit
		} else {
			out.Chunks = chunks
		}
		// omitempty drops an empty chunks array, which would be
		// indistinguishable from "not requested" — say it out loud instead.
		if len(out.Chunks) == 0 {
			msg := "control has no gold chunks"
			if offset > 0 {
				msg = fmt.Sprintf("offset %d is past the control's last chunk", offset)
			}
			out.Gaps = append(out.Gaps, SearchGap{Kind: "no_chunks", Message: msg, BlocksAnswer: false})
		}
	}

	return out, nil
}

// --- DB helpers ---

// controlAmendments returns amendment rows (doc_role 'amendment', same
// framework and version) whose amends_citation_norm targets ctrl. For a
// control that is itself an amendment row, this returns nothing — patches
// target base citations.
func (dc *dbCorpus) controlAmendments(ctx context.Context, ctrl ControlDetail) ([]AmendmentRef, error) {
	const sql = `
SELECT sc.citation, sc.amend_action, d.qualifier, d.doc_key, sc.title, COALESCE(sc.body, '')
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
WHERE d.framework_code = $1
  AND d.version_label = $2
  AND d.doc_role = 'amendment'
  AND sc.amends_citation_norm = $3
ORDER BY d.qualifier, sc.citation`
	rows, err := dc.pool.Query(ctx, sql, ctrl.FrameworkCode, ctrl.VersionLabel, ctrl.CitationNorm)
	if err != nil {
		return nil, fmt.Errorf("query amendments: %w", err)
	}
	defer rows.Close()

	var out []AmendmentRef
	for rows.Next() {
		var a AmendmentRef
		var action *string
		if err := rows.Scan(&a.Citation, &action, &a.Qualifier, &a.DocKey, &a.Title, &a.Body); err != nil {
			return nil, fmt.Errorf("scan amendment: %w", err)
		}
		if action != nil {
			a.Action = *action
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// citationVersions lists version labels (per framework when pinned) whose
// documents carry the citation — for the version_not_found diagnostic.
func (dc *dbCorpus) citationVersions(ctx context.Context, citation, frameworkCode string) ([]string, error) {
	sql := `
SELECT DISTINCT d.framework_code || ' ' || d.version_label
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
WHERE (sc.citation_norm = upper($1) OR sc.citation_norm = upper($2))`
	args := []any{citation, zeroPadCitation(citation)}
	if frameworkCode != "" {
		sql += ` AND d.framework_code = $3`
		args = append(args, frameworkCode)
	}
	sql += ` ORDER BY 1 LIMIT 10`
	rows, err := dc.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query citation versions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// citationFrameworks lists OTHER frameworks whose current version carries the
// citation — for the ambiguous_citation notice.
func (dc *dbCorpus) citationFrameworks(ctx context.Context, citation, chosenFramework string) ([]string, error) {
	rows, err := dc.pool.Query(ctx, `
SELECT DISTINCT d.framework_code
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code AND fv.version_label = d.version_label
WHERE (sc.citation_norm = upper($1) OR sc.citation_norm = upper($2))
  AND fv.is_current AND d.framework_code <> $3
ORDER BY 1 LIMIT 10`, citation, zeroPadCitation(citation), chosenFramework)
	if err != nil {
		return nil, fmt.Errorf("query citation frameworks: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DocumentSourceURLs maps silver document IDs to the official publisher page
// recorded in bronze provenance (empty entries are skipped).
func (dc *dbCorpus) DocumentSourceURLs(ctx context.Context, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	rows, err := dc.pool.Query(ctx, `
SELECT d.id,
       COALESCE((SELECT sf.source_url FROM bronze.source_file sf
                 WHERE sf.sha256 = d.source_file_sha256 AND sf.source_url <> ''
                 LIMIT 1), '')
FROM silver.document d
WHERE d.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("query document source urls: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var url string
		if err := rows.Scan(&id, &url); err != nil {
			return nil, err
		}
		if url != "" {
			out[id] = url
		}
	}
	return out, rows.Err()
}

// FrameworkVersions maps framework code → version labels present in silver.
func (dc *dbCorpus) FrameworkVersions(ctx context.Context) (map[string][]string, error) {
	rows, err := dc.pool.Query(ctx,
		`SELECT DISTINCT framework_code, version_label FROM silver.document ORDER BY framework_code, version_label`)
	if err != nil {
		return nil, fmt.Errorf("query framework versions: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var fw, vl string
		if err := rows.Scan(&fw, &vl); err != nil {
			return nil, err
		}
		out[fw] = append(out[fw], vl)
	}
	return out, rows.Err()
}

func (dc *dbCorpus) findControl(ctx context.Context, citation, frameworkCode, versionLabel string) (ControlDetail, bool, error) {
	var conds []string
	var args []any
	p := 1

	// Exact match on citation_norm (case-insensitive). Also try replacing
	// bare single-digit family numbers with zero-padded (e.g. AC-2 -> AC-02)
	// to handle both agent input styles.
	conds = append(conds, fmt.Sprintf("(sc.citation_norm = upper($%d) OR sc.citation_norm = upper($%d))", p, p+1))
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
    d.serve_gate,
    COALESCE((SELECT sf.source_url FROM bronze.source_file sf
              WHERE sf.sha256 = d.source_file_sha256 AND sf.source_url <> ''
              LIMIT 1), '')
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code
 AND fv.version_label = d.version_label
WHERE %s
ORDER BY fv.is_current DESC, (d.doc_role = 'main') DESC, d.framework_code, sc.citation_norm, sc.id
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
		&ctrl.SourceURL,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ControlDetail{}, false, nil
		}
		return ControlDetail{}, false, fmt.Errorf("find control: %w", err)
	}
	ctrl.VersionStatus = versionStatus(ctrl.IsCurrent)
	return ctrl, true, nil
}

// reDotEnhancement matches the dot-separated enhancement notation some humans
// type for 800-53-style citations: "AC-2.3" (canonical form is "AC-2(3)").
var reDotEnhancement = regexp.MustCompile(`^([A-Za-z]{2,3}-\d+)\.(\d+)$`)

// normalizeCitationInput cleans common human citation typography before the
// DB lookup: internal whitespace ("AC-2 (3)" → "AC-2(3)") and dot-separated
// enhancements ("AC-2.3" → "AC-2(3)"). Dotted decimal citations without a
// letter prefix (PCI "8.3.6", ISO "7.5.1") are left untouched.
func normalizeCitationInput(s string) string {
	s = strings.Join(strings.Fields(s), "")
	if m := reDotEnhancement.FindStringSubmatch(s); m != nil {
		s = m[1] + "(" + m[2] + ")"
	}
	return s
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
		if errors.Is(err, pgx.ErrNoRows) {
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
ORDER BY ordinal, id
LIMIT 500`

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

	// limit+1 probe: fetch one extra row so NextOffset is set only when a
	// further page actually exists (an exact-limit final page must not
	// produce a wasted empty follow-up call).
	rows, err := dc.pool.Query(ctx, q, controlID, limit+1, offset)
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
