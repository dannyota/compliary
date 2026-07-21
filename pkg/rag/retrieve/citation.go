package retrieve

import (
	"regexp"
	"strings"
)

// CitationMatch is one citation pattern found in a query.
type CitationMatch struct {
	Scheme   string // citation_scheme from config.framework
	Citation string // the matched citation text, normalized
}

// schemeEntry pairs a citation_scheme with its compiled regex.
type schemeEntry struct {
	scheme  string
	pattern *regexp.Regexp
}

// schemePatterns is the ordered list of citation scheme regexes, sorted by
// specificity (longest/most-constrained first) as required by the design
// doc (RETRIEVAL.md). When no framework filter is set, all schemes are
// tried in this order and matches are returned deterministically.
var schemePatterns = []schemeEntry{
	// COBIT: EDM01.01, APO12.03, DSS05.01 — 3 uppercase letters + 2 digits + dot + 2 digits.
	{"cobit-objective", regexp.MustCompile(`\b([A-Z]{3}\d{2}\.\d{2})\b`)},
	// CSF: PR.AA-01, GV.OC-02 — 2 uppercase letters + dot + 2 uppercase + hyphen + 2 digits.
	{"csf-workbook", regexp.MustCompile(`\b([A-Z]{2}\.[A-Z]{2}-\d{2})\b`)},
	// OSCAL: AC-2, AC-2(3), SA-11(8) — 2 uppercase letters + hyphen + 1-2 digits + optional parens.
	// \b doesn't work after ')' since ')' is not a word char; use lookahead-free trailing boundary.
	{"oscal-catalog", regexp.MustCompile(`\b([A-Z]{2}-\d{1,2}(?:\(\d+\))?)(?:\b|$|[^A-Za-z0-9(])`)},
	// CCM: AIS-01, DSP-17, IAM-04, A&A-01, I&S-05 — 2-5 uppercase letters
	// (with optional ampersand for A&A, I&S domains) + hyphen + 2 digits.
	{"ccm-workbook", regexp.MustCompile(`\b([A-Z][A-Z&]{1,4}-\d{2})\b`)},
	// TSC: CC6.1, CC7.2, A1.1, PI1.1.
	{"tsc-criteria", regexp.MustCompile(`\b((?:CC|A|PI|P|C)\d+\.\d+)\b`)},
	// PCI: "Req 8.3.6", "1.2.1", "12.3.4".
	{"pci-requirement", regexp.MustCompile(`(?i)\b(?:Req(?:uirement)?\s+)?(\d{1,2}\.\d+(?:\.\d+)*)\b`)},
	// ISO AMS: A.5.1, A.8.24, 5.1, 6.1.2 — optional letter prefix + numeric.
	{"iso-ams", regexp.MustCompile(`\b([A-Z]?\.\d+(?:\.\d+)*|\d+\.\d+(?:\.\d+)?)\b`)},
	// ISO control catalog: 5.1, 8.24, CLD.12.4.1.
	{"iso-control-catalog", regexp.MustCompile(`\b((?:CLD\.)?\d+(?:\.\d+){1,3})\b`)},
	// CIS: 4.1, 16.12, 1.1.1.
	{"cis-workbook", regexp.MustCompile(`\b(\d{1,2}(?:\.\d{1,2}){1,2})\b`)},
	// CSCF: 1.1, 2.8A, 3.1.
	{"cscf-control", regexp.MustCompile(`\b(\d\.\d+[A-Z]?)\b`)},
}

// schemePatternMap provides O(1) scheme->pattern lookup for the
// framework-filtered path.
var schemePatternMap = func() map[string]*regexp.Regexp {
	m := make(map[string]*regexp.Regexp, len(schemePatterns))
	for _, e := range schemePatterns {
		m[e.scheme] = e.pattern
	}
	return m
}()

// MatchCitation extracts citation-shaped tokens from a query. If framework is
// non-empty, only the scheme for that framework is tested; otherwise all
// schemes are tried and all matches returned. The returned slice may have
// duplicates removed; the caller de-dupes by citation_norm in the DB lookup.
func MatchCitation(query string, framework string, frameworkScheme map[string]string) []CitationMatch {
	upper := strings.ToUpper(query)
	var matches []CitationMatch

	if framework != "" {
		scheme, ok := frameworkScheme[framework]
		if !ok {
			return nil
		}
		pat, ok := schemePatternMap[scheme]
		if !ok {
			return nil
		}
		for _, m := range pat.FindAllStringSubmatch(upper, -1) {
			if len(m) > 1 {
				matches = append(matches, CitationMatch{Scheme: scheme, Citation: m[1]})
			}
		}
		return dedupeMatches(matches)
	}

	// No framework filter: try all schemes in specificity order.
	for _, entry := range schemePatterns {
		for _, m := range entry.pattern.FindAllStringSubmatch(upper, -1) {
			if len(m) > 1 {
				matches = append(matches, CitationMatch{Scheme: entry.scheme, Citation: m[1]})
			}
		}
	}
	return dedupeMatches(matches)
}

func dedupeMatches(ms []CitationMatch) []CitationMatch {
	if len(ms) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ms))
	out := make([]CitationMatch, 0, len(ms))
	for _, m := range ms {
		key := m.Scheme + "|" + m.Citation
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}
