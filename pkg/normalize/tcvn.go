package normalize

import (
	"strconv"
	"strings"
)

// tcvnCapture mirrors the extract-stage page capture shape shared by
// pdf-pages-json and txt-pages-json.
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

// parseTCVNPages scans all pages in document order for numbered headings and
// collects the verbatim body text that follows each. It is the shared engine
// behind both TCVN parsers: the caller injects the per-framework skip and
// classify functions (which encode the different heading ranges and structural
// artifacts), while the line-buffering, first-occurrence, and body-collection
// logic is identical across frameworks.
//
//   - skipFn filters structural artifacts (page headers, ToC entries, bare page
//     numbers) from the line stream.
//   - classifyFn returns the citation + heading title if a line opens a numbered
//     heading, else empty strings.
//
// Each citation is kept once (first occurrence). Pass 2 collects the verbatim
// body for each heading (lines up to the next distinct heading occurrence). A
// DUPLICATE citation line is dropped from the body ONLY when it exactly
// reproduces its own heading line (a repeated running header); a
// duplicate-citation line whose text differs is genuine verbatim body content (a
// wrapped line that opens with a number, or a source numbering collision) and is
// preserved, or the verbatim-body invariant breaks.
func parseTCVNPages(pages []tcvnPage, skipFn func(string) bool, classifyFn func(string) (citation, title string)) []tcvnHeading {
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
			if skipFn(s) {
				continue
			}
			cit, title := classifyFn(s)
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
	headingLine := make(map[string]string) // citation -> verbatim heading line text
	for i, li := range lines {
		if li.citation == "" || seen[li.citation] {
			continue
		}
		seen[li.citation] = true
		headingLine[li.citation] = li.text
		starts = append(starts, start{lineIdx: i, citation: li.citation, title: li.title})
	}

	// Pass 2: collect the verbatim body for each heading (lines up to the next
	// distinct heading occurrence). Nested headings never leak into a parent's
	// body because a new citation ends the region.
	headings := make([]tcvnHeading, 0, len(starts))
	for k, st := range starts {
		endIdx := len(lines)
		if k+1 < len(starts) {
			endIdx = starts[k+1].lineIdx
		}
		var bodyParts []string
		for j := st.lineIdx + 1; j < endIdx; j++ {
			if c := lines[j].citation; c != "" && headingLine[c] == lines[j].text {
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

// buildTCVNTree assembles the normalized control tree from parsed headings,
// shared by both TCVN parsers (only the tree Title differs per framework).
//
// Structural leaf detection: a citation is intermediate (kind 'clause') iff
// another detected citation has it as a dotted prefix; otherwise it is a leaf
// (kind 'control'). This handles the differing leaf depths uniformly and makes a
// silently dropped requirement detectable by enumeration.
//
// citation_norm = the printed clause number exactly. title (public-safe) is a
// neutral generated label; title_original carries the verbatim Vietnamese
// heading and body carries the verbatim Vietnamese clause text — both gated by
// the document's serve_gate downstream.
func buildTCVNTree(headings []tcvnHeading, title string) *TreeResult {
	result := &TreeResult{Title: title}

	// A citation is intermediate iff another citation has it as a dotted prefix.
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
			t := h.Title
			cr.TitleOriginal = &t
		}
		// Verbatim Vietnamese clause body (intermediate headings have none).
		if h.Body != "" {
			body := h.Body
			cr.Body = &body
		}

		// Parent = citation with the last dotted segment removed. Section/level
		// roots resolve to a non-heading prefix and become roots.
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

	return result
}
