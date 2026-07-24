package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// tcvnCapture mirrors extract.PDFCapture for parsing pdf-pages-json.
type tcvnCapture struct {
	Pages []tcvnPage `json:"pages"`
}

type tcvnPage struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// tcvnHeading is an intermediate parse result: a numbered heading line and the
// verbatim body text that follows it (up to the next heading).
type tcvnHeading struct {
	Citation string // e.g. "5.1.1.2" or "A.2.10"
	Title    string // verbatim Vietnamese heading text after the number
	Body     string // verbatim Vietnamese body text following the heading
}

// reTCVNMain matches a main-body numbered heading at line start: a dotted
// number (1–4 segments) followed by heading text. The top segment is validated
// separately to the security-level range 5–9; front matter (1–4) and term
// definitions (3.x) are rejected there.
var reTCVNMain = regexp.MustCompile(`^(\d+(?:\.\d+){0,3})\s+(\S.*)$`)

// reTCVNAnnex matches an Annex A heading at line start: A.N or A.N.M followed
// by heading text (e.g. "A.1 ..." level heading, "A.2.10 ..." leaf clause).
var reTCVNAnnex = regexp.MustCompile(`^(A\.\d+(?:\.\d+)?)\s+(\S.*)$`)

// reTCVNBarePageNum matches a bare page-number line.
var reTCVNBarePageNum = regexp.MustCompile(`^\d+$`)

// BuildTCVN11930Tree parses a pdf-pages-json capture for TCVN 11930:2017 and
// returns the normalized control tree. Pure function, no side effects.
//
// Tree shape (citation scheme 'tcvn-clause'):
//   - Intermediate numbered headings — the security levels (5–9), their
//     management/technical groups (X.1 / X.2), requirement subgroups (X.Y.Z),
//     and the Annex A level headings (A.1–A.5) — become kind 'clause'.
//   - Leaf numbered requirement clauses — X.Y.Z.W in the main body and A.N.M in
//     Annex A, i.e. any numbered heading with no numbered children — become the
//     citable unit kind 'control'.
//
// A node is a leaf iff no other detected citation has it as a dotted prefix;
// this handles the two differing leaf depths (X.Y.Z.W vs A.N.M) uniformly and
// makes a silently dropped requirement detectable by enumeration.
//
// citation_norm = the printed clause number exactly. title (public-safe) is a
// neutral generated label; title_original carries the verbatim Vietnamese
// heading and body carries the verbatim Vietnamese clause text — both gated by
// the document's serve_gate. Front matter (foreword, scope, references, terms
// 1–4) is not emitted, matching the ISO/PCI convention.
func BuildTCVN11930Tree(raw json.RawMessage, _, versionLabel string) (*TreeResult, error) {
	var cap tcvnCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}
	if len(cap.Pages) == 0 {
		return nil, fmt.Errorf("no pages in capture")
	}

	headings := parseTCVNHeadings(cap.Pages)
	if len(headings) == 0 {
		return nil, fmt.Errorf("no numbered requirement clauses found in capture")
	}

	result := &TreeResult{
		Title: "TCVN 11930 " + versionLabel,
	}

	// Structural leaf detection: a citation is intermediate iff another citation
	// has it as a dotted prefix.
	citSet := make(map[string]bool, len(headings))
	for _, h := range headings {
		citSet[h.Citation] = true
	}
	isIntermediate := func(c string) bool {
		prefix := c + "."
		for other := range citSet {
			if strings.HasPrefix(other, prefix) {
				return true
			}
		}
		return false
	}

	citationToIdx := make(map[string]int, len(headings))
	var ordinal int32
	for _, h := range headings {
		kind := "control"
		titlePrefix := "Control "
		if isIntermediate(h.Citation) {
			kind = "clause"
			titlePrefix = "Clause "
		}

		cr := ControlRow{
			Citation:     h.Citation,
			CitationNorm: h.Citation, // already the canonical printed number
			Kind:         kind,
			Status:       "active",
			Title:        titlePrefix + h.Citation,
			Ordinal:      ordinal,
		}

		// Verbatim Vietnamese heading (gated by serve_gate downstream).
		if h.Title != "" {
			title := h.Title
			cr.TitleOriginal = &title
		}
		// Verbatim Vietnamese clause body (intermediate headings have none).
		if h.Body != "" {
			body := h.Body
			cr.Body = &body
		}

		// Parent = citation with the last dotted segment removed. Annex level
		// headings (A.N) and main levels (N) resolve to a non-heading prefix
		// and become roots.
		parentCite := tcvnParentCitation(h.Citation)
		if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		citationToIdx[h.Citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

// parseTCVNHeadings scans all pages in document order for numbered headings in
// the normative range (main-body levels 5–9 and Annex A), collecting the
// verbatim body text that follows each. Front matter, table-of-contents lines,
// and page-header artifacts are filtered. Each citation is kept once (first
// occurrence).
func parseTCVNHeadings(pages []tcvnPage) []tcvnHeading {
	type line struct {
		text     string
		citation string // non-empty if this line opens a heading
		title    string
	}

	var lines []line
	for _, p := range pages {
		for _, raw := range strings.Split(p.Text, "\n") {
			s := strings.TrimSpace(raw)
			if s == "" {
				continue
			}
			if isTCVNSkipLine(s) {
				continue
			}
			cit, title := classifyTCVNLine(s)
			lines = append(lines, line{text: s, citation: cit, title: title})
		}
	}

	// Pass 1: record the first occurrence of each heading citation, in order.
	type start struct {
		lineIdx  int
		citation string
		title    string
	}
	var starts []start
	seen := make(map[string]bool)
	for i, li := range lines {
		if li.citation == "" || seen[li.citation] {
			continue
		}
		seen[li.citation] = true
		starts = append(starts, start{lineIdx: i, citation: li.citation, title: li.title})
	}

	// Pass 2: collect the verbatim body for each heading (lines up to the next
	// heading occurrence). Non-heading lines only — nested headings never leak
	// into a parent's body.
	headings := make([]tcvnHeading, 0, len(starts))
	for k, st := range starts {
		endIdx := len(lines)
		if k+1 < len(starts) {
			endIdx = starts[k+1].lineIdx
		}
		var bodyParts []string
		for j := st.lineIdx + 1; j < endIdx; j++ {
			if lines[j].citation != "" {
				continue
			}
			bodyParts = append(bodyParts, lines[j].text)
		}
		headings = append(headings, tcvnHeading{
			Citation: st.citation,
			Title:    strings.TrimSpace(st.title),
			Body:     strings.TrimSpace(strings.Join(bodyParts, "\n")),
		})
	}

	return headings
}

// classifyTCVNLine returns the citation + heading title if the line opens a
// normative numbered heading (main-body level 5–9 or Annex A), else empty
// strings. Leading-zero segments (e.g. a wrapped "06 thang ..." body line) are
// rejected so body text is never mistaken for a clause number.
func classifyTCVNLine(s string) (citation, title string) {
	if m := reTCVNAnnex.FindStringSubmatch(s); m != nil {
		if tcvnCanonicalNumber(m[1]) {
			return m[1], m[2]
		}
		return "", ""
	}
	if m := reTCVNMain.FindStringSubmatch(s); m != nil {
		cit := m[1]
		switch strings.SplitN(cit, ".", 2)[0] {
		case "5", "6", "7", "8", "9":
			if tcvnCanonicalNumber(cit) {
				return cit, m[2]
			}
		}
	}
	return "", ""
}

// tcvnCanonicalNumber reports whether every numeric segment of a dotted
// citation is a canonical integer (no leading zero). "A.2.10" and "5.1.1.2" are
// canonical; "06" and "5.01" are not.
func tcvnCanonicalNumber(citation string) bool {
	for _, seg := range strings.Split(citation, ".") {
		if seg == "A" {
			continue
		}
		n, err := strconv.Atoi(seg)
		if err != nil {
			return false
		}
		if strconv.Itoa(n) != seg {
			return false
		}
	}
	return true
}

// tcvnParentCitation returns the parent citation by dropping the last dotted
// segment. "5.1.1.2" -> "5.1.1"; "A.2.10" -> "A.2"; "5" -> ""; "A.1" -> "A".
// Callers treat a parent that is not itself a heading as a root.
func tcvnParentCitation(citation string) string {
	lastDot := strings.LastIndex(citation, ".")
	if lastDot < 0 {
		return ""
	}
	return citation[:lastDot]
}

// isTCVNSkipLine filters structural artifacts (running page header, bare page
// numbers, table-of-contents dot-leader lines) from the line stream.
func isTCVNSkipLine(line string) bool {
	if strings.Contains(line, "TCVN 11930") {
		return true
	}
	if strings.Contains(line, "T I E U") || strings.Contains(line, "T I Ê U") {
		return true
	}
	if strings.Contains(line, "All rights reserved") {
		return true
	}
	// Table-of-contents lines carry dot leaders.
	if strings.Contains(line, "...") {
		return true
	}
	if reTCVNBarePageNum.MatchString(line) {
		return true
	}
	return false
}
