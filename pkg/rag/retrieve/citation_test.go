package retrieve

import (
	"testing"
)

// testSchemes maps framework_code -> citation_scheme for tests.
var testSchemes = map[string]string{
	"nist80053":   "oscal-catalog",
	"nistcsf":     "csf-workbook",
	"ciscontrols": "cis-workbook",
	"pcidss":      "pci-requirement",
	"soc2tsc":     "tsc-criteria",
	"iso27001":    "iso-ams",
	"iso27002":    "iso-control-catalog",
	"iso27017":    "iso-control-catalog",
	"iso27018":    "iso-control-catalog",
	"csaccm":      "ccm-workbook",
	"cobit":       "cobit-objective",
	"swiftcscf":   "cscf-control",
}

func TestMatchCitation_OSCAL(t *testing.T) {
	tests := []struct {
		query  string
		fw     string
		expect []string
	}{
		{"What is AC-2?", "nist80053", []string{"AC-2"}},
		{"Tell me about AC-2(3)", "nist80053", []string{"AC-2(3)"}},
		{"SA-11(8) enhancement", "nist80053", []string{"SA-11(8)"}},
		{"Controls AC-2 and SC-7", "nist80053", []string{"AC-2", "SC-7"}},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, tc.fw, testSchemes)
		if len(ms) != len(tc.expect) {
			t.Errorf("%q: expected %d matches, got %d: %v", tc.query, len(tc.expect), len(ms), ms)
			continue
		}
		for i, m := range ms {
			if m.Citation != tc.expect[i] {
				t.Errorf("%q: match[%d] = %q, want %q", tc.query, i, m.Citation, tc.expect[i])
			}
			if m.Scheme != "oscal-catalog" {
				t.Errorf("%q: scheme = %q, want oscal-catalog", tc.query, m.Scheme)
			}
		}
	}
}

func TestMatchCitation_CSF(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"What is PR.AA-01?", "PR.AA-01"},
		{"GV.OC-02 subcategory", "GV.OC-02"},
		{"DE.AE-02 analysis", "DE.AE-02"},
		{"ID.AM-07 vulnerability", "ID.AM-07"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "nistcsf", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_TSC(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"CC6.1 access", "CC6.1"},
		{"CC7.2 monitoring", "CC7.2"},
		{"A1.1 availability", "A1.1"},
		{"PI1.1 integrity", "PI1.1"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "soc2tsc", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_PCI(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"Requirement 8.3.6", "8.3.6"},
		{"Req 1.2.1", "1.2.1"},
		{"What is requirement 12.3.4?", "12.3.4"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "pcidss", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_CCM(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"AIS-01 security", "AIS-01"},
		{"DSP-17 privacy", "DSP-17"},
		{"IAM-04 separation", "IAM-04"},
		{"What does A&A-03 cover?", "A&A-03"},
		{"Describe I&S-09 scope", "I&S-09"},
		{"A&A-01 audit", "A&A-01"},
		{"I&S-05 infrastructure", "I&S-05"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "csaccm", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_COBIT(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"EDM01.01 governance", "EDM01.01"},
		{"APO12.03 risk", "APO12.03"},
		{"DSS05.01 security", "DSS05.01"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "cobit", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_ISO_AMS(t *testing.T) {
	tests := []struct {
		query  string
		expect string
	}{
		{"A.5.1 policies", "A.5.1"},
		{"A.8.24 filtering", "A.8.24"},
	}
	for _, tc := range tests {
		ms := MatchCitation(tc.query, "iso27001", testSchemes)
		if len(ms) == 0 {
			t.Errorf("%q: no matches", tc.query)
			continue
		}
		if ms[0].Citation != tc.expect {
			t.Errorf("%q: got %q, want %q", tc.query, ms[0].Citation, tc.expect)
		}
	}
}

func TestMatchCitation_NoFramework_MultiScheme(t *testing.T) {
	// Bare "5.1" without a framework filter should match multiple schemes.
	ms := MatchCitation("What is 5.1?", "", testSchemes)
	if len(ms) == 0 {
		t.Fatal("expected matches for bare 5.1, got none")
	}
	// Should find matches from multiple schemes.
	schemes := make(map[string]bool, len(ms))
	for _, m := range ms {
		schemes[m.Scheme] = true
	}
	if len(schemes) < 2 {
		t.Errorf("expected matches from multiple schemes for bare 5.1, got %d schemes", len(schemes))
	}
}

func TestMatchCitation_NoMatch(t *testing.T) {
	ms := MatchCitation("What is chocolate cake?", "", testSchemes)
	if len(ms) != 0 {
		t.Errorf("expected no matches for out-of-scope query, got %d", len(ms))
	}
}

func TestMatchCitation_UnknownFramework(t *testing.T) {
	ms := MatchCitation("AC-2", "unknown_fw", testSchemes)
	if len(ms) != 0 {
		t.Errorf("expected no matches for unknown framework, got %d", len(ms))
	}
}

func TestMatchCitation_NoFramework_DeterministicOrder(t *testing.T) {
	// Without a framework filter, matches must be returned in the
	// documented specificity order (most-constrained scheme first).
	// Run the matcher many times to confirm the order is stable
	// (the prior map-based iteration was nondeterministic).
	query := "Tell me about AC-2 and 5.1 and PR.AA-01"
	var baseline []CitationMatch
	for i := 0; i < 50; i++ {
		ms := MatchCitation(query, "", testSchemes)
		if i == 0 {
			baseline = ms
			if len(baseline) == 0 {
				t.Fatal("expected matches, got none")
			}
			continue
		}
		if len(ms) != len(baseline) {
			t.Fatalf("run %d: got %d matches, baseline had %d", i, len(ms), len(baseline))
		}
		for j, m := range ms {
			if m.Scheme != baseline[j].Scheme || m.Citation != baseline[j].Citation {
				t.Fatalf("run %d: match[%d] = {%s %s}, baseline = {%s %s}",
					i, j, m.Scheme, m.Citation, baseline[j].Scheme, baseline[j].Citation)
			}
		}
	}

	// Verify that more-specific schemes appear before less-specific ones.
	// "PR.AA-01" matches csf-workbook (index 1 in schemePatterns);
	// "AC-2" matches oscal-catalog (index 2);
	// "5.1" matches several broad schemes.
	// csf-workbook should precede oscal-catalog in the output.
	csfIdx, oscalIdx := -1, -1
	for i, m := range baseline {
		if m.Scheme == "csf-workbook" && csfIdx == -1 {
			csfIdx = i
		}
		if m.Scheme == "oscal-catalog" && oscalIdx == -1 {
			oscalIdx = i
		}
	}
	if csfIdx == -1 || oscalIdx == -1 {
		t.Fatalf("expected both csf-workbook and oscal-catalog matches, csf=%d oscal=%d", csfIdx, oscalIdx)
	}
	if csfIdx > oscalIdx {
		t.Errorf("csf-workbook (specificity 2) should precede oscal-catalog (specificity 3): csf@%d oscal@%d",
			csfIdx, oscalIdx)
	}
}
