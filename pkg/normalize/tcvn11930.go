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

// reTCVNPageHeader matches the running page-header line: the standard's
// identifier alone on a line. Whole-line anchored so a self-reference to
// "TCVN 11930" inside requirement text is not stripped from the body.
var reTCVNPageHeader = regexp.MustCompile(`^TCVN 11930:\d{4}$`)

// reTCVNTOCLine matches a table-of-contents entry: a dot-leader run followed by
// a trailing page number. Anchored to the leader+number tail so an inline
// ellipsis inside requirement text (e.g. "(mật khẩu, mã số truy cập...)") is
// not stripped from the body.
var reTCVNTOCLine = regexp.MustCompile(`\.{2,}\s*\d+\s*$`)

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
func BuildTCVN11930Tree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	// This parser is specific to TCVN 11930:2017's PDF layout. The tcvn-clause
	// scheme is shared with tcvn14423 (a different capture), so guard here as
	// the ISO control-catalog parser does for its scheme.
	if frameworkCode != "tcvn11930" {
		return nil, fmt.Errorf("unsupported framework for tcvn11930 parser: %s", frameworkCode)
	}

	var capture tcvnCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}
	if len(capture.Pages) == 0 {
		return nil, fmt.Errorf("no pages in capture")
	}

	headings := parseTCVNHeadings(capture.Pages)
	if len(headings) == 0 {
		return nil, fmt.Errorf("no numbered requirement clauses found in capture")
	}

	return buildTCVNTree(headings, "TCVN 11930 "+versionLabel), nil
}

// parseTCVNHeadings scans all pages in document order for numbered headings in
// the normative range (main-body levels 5–9 and Annex A), collecting the
// verbatim body text that follows each. Front matter, table-of-contents lines,
// and page-header artifacts are filtered. Each citation is kept once (first
// occurrence). The shared engine is parseTCVNPages; the 11930-specific skip and
// classify functions encode this document's heading ranges and artifacts.
func parseTCVNHeadings(pages []tcvnPage) []tcvnHeading {
	return parseTCVNPages(pages, isTCVNSkipLine, classifyTCVNLine)
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
	// Running page header: the standard identifier alone on a line (whole-line
	// match — a "TCVN 11930" self-reference inside requirement text survives).
	if reTCVNPageHeader.MatchString(line) {
		return true
	}
	// Spaced running-title banner on title / section-break pages.
	if strings.Contains(line, "T I E U") || strings.Contains(line, "T I Ê U") {
		return true
	}
	if strings.Contains(line, "All rights reserved") {
		return true
	}
	// Table-of-contents entry: dot-leader run + trailing page number (anchored —
	// an inline ellipsis inside requirement text survives).
	if reTCVNTOCLine.MatchString(line) {
		return true
	}
	if reTCVNBarePageNum.MatchString(line) {
		return true
	}
	return false
}
