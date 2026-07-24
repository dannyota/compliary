package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// reTCVN14423Heading matches a numbered heading in the normative sections 4 or
// 5 at line start: a dotted number whose top segment is 4 or 5 (1–4 segments),
// followed by whitespace and heading text. Section/family headings in section 5
// are tab-separated in the source and leaves are space-separated, so \s+ covers
// both. Front matter (sections 1–3, including term definitions 3.x and the 3.2
// abbreviation table) is excluded by the 4|5 top-segment anchor. Leading-zero
// segments are rejected in classifyTCVN14423Line via tcvnCanonicalNumber.
var reTCVN14423Heading = regexp.MustCompile(`^([45](?:\.\d+){0,3})\s+(\S.*)$`)

// reTCVN14423TOCEntry matches a table-of-contents entry: a heading line ending
// in a tab followed by a page number. Anchored to the trailing tab+page-number
// tail so no section-4/5 requirement line is stripped (verified: no body line
// in the corpus ends this way). The document's ToC uses "citation\ttitle\tN"
// rows, so this removes the 4.x/5.x ToC entries whose first occurrence would
// otherwise win over the real body heading.
var reTCVN14423TOCEntry = regexp.MustCompile(`\t\d+\s*$`)

// reTCVN14423PageHeader matches the running/title page header: the standard's
// identifier alone on a line. Whole-line anchored so a "TCVN 14423:2025"
// self-reference inside requirement text is not stripped from the body.
var reTCVN14423PageHeader = regexp.MustCompile(`^TCVN 14423:\d{4}$`)

// BuildTCVN14423Tree parses a txt-pages-json capture for TCVN 14423:2025 and
// returns the normalized control tree. Pure function, no side effects.
//
// Tree shape (citation scheme 'tcvn-clause'):
//   - Section roots 4 (state-agency information systems) and 5 (national-security
//     critical information systems), their requirement families N.M, and the
//     "Yeu cau cu the" subgroups N.M.2 that have numbered children become kind
//     'clause'.
//   - Leaf numbered clauses — the "Khai quat" overview clause N.M.1, the specific
//     requirement leaves N.M.2.x, and any N.M.2 group written without numbered
//     children (its requirement is inline body) — become the citable unit kind
//     'control'.
//
// A node is a leaf iff no other detected citation has it as a dotted prefix;
// this handles the two leaf depths uniformly (N.M.1 vs N.M.2.x) and the
// document's structural exception (an N.M.2 with no children) without special
// casing, and makes a silently dropped requirement detectable by enumeration.
//
// citation_norm = the printed clause number exactly. title (public-safe) is a
// neutral generated label; title_original carries the verbatim Vietnamese
// heading and body carries the verbatim Vietnamese clause text — both gated by
// the document's serve_gate. Front matter (sections 1–3) is not emitted,
// matching the ISO/PCI/TCVN 11930 convention.
func BuildTCVN14423Tree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	// This parser is specific to TCVN 14423:2025's txt layout. The tcvn-clause
	// scheme is shared with tcvn11930 (a different capture), so guard here as the
	// sibling parsers do for their schemes.
	if frameworkCode != "tcvn14423" {
		return nil, fmt.Errorf("unsupported framework for tcvn14423 parser: %s", frameworkCode)
	}

	// txt-pages-json shares the {pages:[{n,text}]} shape with pdf-pages-json, so
	// the generic tcvnCapture/tcvnPage types deserialize it directly.
	var capture tcvnCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		return nil, fmt.Errorf("unmarshal txt capture: %w", err)
	}
	if len(capture.Pages) == 0 {
		return nil, fmt.Errorf("no pages in capture")
	}

	headings := parseTCVN14423Headings(capture.Pages)
	if len(headings) == 0 {
		return nil, fmt.Errorf("no numbered requirement clauses found in capture")
	}

	return buildTCVNTree(headings, "TCVN 14423 "+versionLabel), nil
}

// parseTCVN14423Headings scans all pages in document order for numbered headings
// in sections 4 and 5, collecting the verbatim body text that follows each.
// Front matter, table-of-contents entries, and the page-header artifact are
// filtered. Each citation is kept once (first occurrence). The shared engine is
// parseTCVNPages; the 14423-specific skip and classify functions encode this
// document's heading ranges and artifacts.
func parseTCVN14423Headings(pages []tcvnPage) []tcvnHeading {
	return parseTCVNPages(pages, isTCVN14423SkipLine, classifyTCVN14423Line)
}

// classifyTCVN14423Line returns the citation + heading title if the line opens a
// numbered heading in sections 4 or 5, else empty strings. Leading-zero
// segments (e.g. a wrapped "03 lan ..." body line) are rejected so body text is
// never mistaken for a clause number.
func classifyTCVN14423Line(s string) (citation, title string) {
	if m := reTCVN14423Heading.FindStringSubmatch(s); m != nil {
		if tcvnCanonicalNumber(m[1]) {
			return m[1], m[2]
		}
	}
	return "", ""
}

// isTCVN14423SkipLine filters structural artifacts (the whole-line page header
// and table-of-contents entries) from the line stream.
func isTCVN14423SkipLine(line string) bool {
	// Whole-line page/title header (a mid-line self-reference survives).
	if reTCVN14423PageHeader.MatchString(line) {
		return true
	}
	// Table-of-contents entry: trailing tab + page number.
	if reTCVN14423TOCEntry.MatchString(line) {
		return true
	}
	return false
}
