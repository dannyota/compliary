package eval

import (
	"strings"
	"testing"

	pkgeval "danny.vn/compliary/pkg/eval"
)

func TestGoldenCSVLoads(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden set is empty")
	}
	t.Logf("golden set: %d cases", len(cases))
}

func TestGoldenCSVNoDuplicateIDs(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	seen := make(map[string]bool)
	for _, c := range cases {
		if seen[c.ID] {
			t.Errorf("duplicate case id: %s", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestGoldenCSVNoLicensedText(t *testing.T) {
	// The golden CSV must never contain verbatim licensed text. Queries must be
	// in our own words. This test is a basic sanity check -- it flags common
	// patterns that suggest copy-pasted normative text.
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	// Licensed text markers: "shall", "must" in prescriptive form are common in
	// ISO/PCI normative text but unlikely in our paraphrased questions.
	// This is a heuristic, not a guarantee.
	for _, c := range cases {
		q := strings.ToLower(c.Question)
		if strings.Contains(q, "the organization shall") ||
			strings.Contains(q, "the entity shall") ||
			strings.Contains(q, "the entity must") {
			t.Errorf("case %s question looks like verbatim licensed text: %s", c.ID, c.Question)
		}
	}
}

func TestGoldenCSVCoversAllDocuments(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}

	// All 11 framework_code values that have ingested documents must appear.
	required := map[string]bool{
		"nist80053":   false,
		"nistcsf":     false,
		"ciscontrols": false,
		"pcidss":      false,
		"soc2tsc":     false,
		"iso27001":    false,
		"iso27002":    false,
		"iso27017":    false,
		"iso27018":    false,
		"csaccm":      false,
		"cobit":       false,
	}

	for _, c := range cases {
		for _, ec := range c.ExpectedCitations {
			fw := strings.ToLower(ec.FrameworkCode)
			if _, ok := required[fw]; ok {
				required[fw] = true
			}
		}
	}

	for fw, covered := range required {
		if !covered {
			t.Errorf("framework %s has no golden cases", fw)
		}
	}
}

func TestGoldenCSVAbstainCases(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}

	var abstainCount int
	for _, c := range cases {
		if c.ExpectAbstain {
			abstainCount++
			if len(c.ExpectedCitations) > 0 {
				t.Errorf("abstain case %s has expected citations", c.ID)
			}
		}
	}
	if abstainCount < 5 {
		t.Errorf("golden set has %d abstain cases, want at least 5", abstainCount)
	}
}

func TestGoldenCSVMinCaseCount(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	if len(cases) < 50 {
		t.Errorf("golden set has %d cases, want at least 50", len(cases))
	}
}

func TestGoldenCSVCollisionCases(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}

	// Verify bare-ID collision cases exist (same citation_norm, different framework).
	type key struct {
		citNorm string
	}
	normFrameworks := make(map[string]map[string]bool)
	for _, c := range cases {
		for _, ec := range c.ExpectedCitations {
			norm := strings.ToLower(ec.CitationNorm)
			if normFrameworks[norm] == nil {
				normFrameworks[norm] = make(map[string]bool)
			}
			normFrameworks[norm][strings.ToLower(ec.FrameworkCode)] = true
		}
	}

	var collisions int
	for _, fws := range normFrameworks {
		if len(fws) > 1 {
			collisions++
		}
	}
	if collisions == 0 {
		t.Error("no bare-ID collision cases found (same citation_norm, different frameworks)")
	}
}

func TestGoldenCSVVersionPinCases(t *testing.T) {
	cases, err := pkgeval.LoadGoldenEmbed(GoldenCSV, "golden.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}

	var versionPinCount int
	for _, c := range cases {
		if strings.HasPrefix(c.ID, "version-") {
			versionPinCount++
		}
	}
	if versionPinCount == 0 {
		t.Error("no version-pin cases found")
	}
}
