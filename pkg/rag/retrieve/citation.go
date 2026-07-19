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

// schemePattern maps a citation_scheme to the regex that recognizes its
// citation format in a query. Patterns are anchored to word boundaries
// where possible. Order is by specificity (longest/most-constrained first).
var schemePattern = map[string]*regexp.Regexp{
	// COBIT: EDM01.01, APO12.03, DSS05.01 — 3 uppercase letters + 2 digits + dot + 2 digits.
	"cobit-objective": regexp.MustCompile(`\b([A-Z]{3}\d{2}\.\d{2})\b`),
	// CSF: PR.AA-01, GV.OC-02 — 2 uppercase letters + dot + 2 uppercase + hyphen + 2 digits.
	"csf-workbook": regexp.MustCompile(`\b([A-Z]{2}\.[A-Z]{2}-\d{2})\b`),
	// OSCAL: AC-2, AC-2(3), SA-11(8) — 2 uppercase letters + hyphen + 1-2 digits + optional parens.
	// \b doesn't work after ')' since ')' is not a word char; use lookahead-free trailing boundary.
	"oscal-catalog": regexp.MustCompile(`\b([A-Z]{2}-\d{1,2}(?:\(\d+\))?)(?:\b|$|[^A-Za-z0-9(])`),
	// CCM: AIS-01, DSP-17, IAM-04 — 2-4 uppercase letters + hyphen + 2 digits.
	"ccm-workbook": regexp.MustCompile(`\b([A-Z]{2,4}-\d{2})\b`),
	// TSC: CC6.1, CC7.2, A1.1, PI1.1.
	"tsc-criteria": regexp.MustCompile(`\b((?:CC|A|PI|P|C)\d+\.\d+)\b`),
	// PCI: "Req 8.3.6", "1.2.1", "12.3.4".
	"pci-requirement": regexp.MustCompile(`(?i)\b(?:Req(?:uirement)?\s+)?(\d{1,2}\.\d+(?:\.\d+)*)\b`),
	// ISO AMS: A.5.1, A.8.24, 5.1, 6.1.2 — optional letter prefix + numeric.
	"iso-ams": regexp.MustCompile(`\b([A-Z]?\.\d+(?:\.\d+)*|\d+\.\d+(?:\.\d+)?)\b`),
	// ISO control catalog: 5.1, 8.24, CLD.12.4.1.
	"iso-control-catalog": regexp.MustCompile(`\b((?:CLD\.)?\d+(?:\.\d+){1,3})\b`),
	// CIS: 4.1, 16.12, 1.1.1.
	"cis-workbook": regexp.MustCompile(`\b(\d{1,2}(?:\.\d{1,2}){1,2})\b`),
	// CSCF: 1.1, 2.8A, 3.1.
	"cscf-control": regexp.MustCompile(`\b(\d\.\d+[A-Z]?)\b`),
}

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
		pat, ok := schemePattern[scheme]
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

	// No framework filter: try all schemes.
	for scheme, pat := range schemePattern {
		for _, m := range pat.FindAllStringSubmatch(upper, -1) {
			if len(m) > 1 {
				matches = append(matches, CitationMatch{Scheme: scheme, Citation: m[1]})
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
