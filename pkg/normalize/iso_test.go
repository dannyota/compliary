package normalize

import (
	"encoding/json"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// --- Synthetic fixtures (ALL WORDING IS INVENTED — no ISO normative text) ---

// syntheticISO27001 is a minimal pdf-pages-json fixture for ISO 27001:2022.
// Covers clause tree (clauses 4-10 with subclauses) and Annex A table
// (4 theme domains + 6 sample controls across themes 5-8).
const syntheticISO27001 = `{
  "pages": [
    {
      "n": 1,
      "text": "ISO/IEC 27001:2022(E)\nForeword\nThis document was prepared by Joint Technical Committee.\n"
    },
    {
      "n": 5,
      "text": "4 Context of the organization\nThe organization shall determine external and internal issues.\n4.1 Understanding the organization and its context\nThe organization shall determine relevant issues.\n4.2 Understanding the needs and expectations\nThe organization shall determine interested parties.\n"
    },
    {
      "n": 6,
      "text": "5 Leadership\nTop management shall demonstrate leadership.\n5.1 Leadership and commitment\nTop management shall demonstrate commitment to the ISMS.\n5.2 Policy\nTop management shall establish an information security policy.\n5.3 Organizational roles and responsibilities\nTop management shall ensure responsibilities are assigned.\n"
    },
    {
      "n": 7,
      "text": "6 Planning\nThe organization shall plan actions.\n6.1 Actions to address risks and opportunities\nThe organization shall determine risks and opportunities.\n6.1.1 General\nWhen planning, the organization shall consider the issues.\n6.1.2 Information security risk assessment\nThe organization shall define a risk assessment process.\n"
    },
    {
      "n": 8,
      "text": "7 Support\nThe organization shall determine resources.\n"
    },
    {
      "n": 9,
      "text": "8 Operation\nThe organization shall plan, implement and control processes.\n"
    },
    {
      "n": 10,
      "text": "9 Performance evaluation\nThe organization shall monitor, measure, analyze and evaluate.\n"
    },
    {
      "n": 11,
      "text": "10 Improvement\nThe organization shall continually improve.\n"
    },
    {
      "n": 17,
      "text": "ISO/IEC 27001:2022(E)\nAnnex A\n(normative)\nTable A.1\n5 Organizational controls\n5.1 Invented policies requirement Invented control text for organizational policy.\n5.2 Invented information security roles Invented control text for roles.\n6 People controls\n6.1 Invented screening Invented control text for screening.\nTable A.1 (continued) Table A.1 (continued)\n"
    },
    {
      "n": 18,
      "text": "ISO/IEC 27001:2022(E)\n7 Physical controls\n7.1 Invented physical security Invented control text for physical security.\n8 Technological controls\n8.1 Invented user endpoint devices Invented control text for endpoints.\n8.2 Invented privileged access Invented control text for access.\nTable A.1 (continued) Table A.1 (continued)\n"
    }
  ]
}`

func TestBuildISO27001Tree_Synthetic(t *testing.T) {
	tree, err := BuildISO27001Tree(json.RawMessage(syntheticISO27001), "iso27001", "2022")
	if err != nil {
		t.Fatalf("BuildISO27001Tree: %v", err)
	}

	// Title.
	if tree.Title != "ISO/IEC 27001 2022" {
		t.Errorf("title=%q, want %q", tree.Title, "ISO/IEC 27001 2022")
	}

	// Expected:
	// Clauses: 4, 4.1, 4.2, 5, 5.1, 5.2, 5.3, 6, 6.1, 6.1.1, 6.1.2, 7, 8, 9, 10 = 15
	// Annex A: domains A.5, A.6, A.7, A.8 = 4
	// Annex A controls: A.5.1, A.5.2, A.6.1, A.7.1, A.8.1, A.8.2 = 6
	// Total: 15 + 4 + 6 = 25
	if len(tree.Controls) != 25 {
		t.Fatalf("controls=%d, want 25; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Verify clause tree structure.
	c4 := tree.Controls[0]
	if c4.Citation != "4" || c4.Kind != "clause" {
		t.Errorf("control[0] citation=%s kind=%s, want 4/clause", c4.Citation, c4.Kind)
	}
	if c4.ParentIdx != -1 {
		t.Errorf("clause 4 parentIdx=%d, want -1", c4.ParentIdx)
	}
	if c4.Title != "Clause 4" {
		t.Errorf("clause 4 title=%q, want 'Clause 4'", c4.Title)
	}

	// 4.1 under 4.
	c41 := tree.Controls[1]
	if c41.Citation != "4.1" || c41.Kind != "clause" {
		t.Errorf("control[1] citation=%s kind=%s, want 4.1/clause", c41.Citation, c41.Kind)
	}
	if c41.ParentIdx != 0 {
		t.Errorf("4.1 parentIdx=%d, want 0 (clause 4)", c41.ParentIdx)
	}

	// 6.1.1 under 6.1.
	var c611Idx int = -1
	var c61Idx int = -1
	for i, c := range tree.Controls {
		if c.Citation == "6.1.1" {
			c611Idx = i
		}
		if c.Citation == "6.1" {
			c61Idx = i
		}
	}
	if c611Idx < 0 || c61Idx < 0 {
		t.Fatal("6.1 or 6.1.1 not found")
	}
	if tree.Controls[c611Idx].ParentIdx != c61Idx {
		t.Errorf("6.1.1 parentIdx=%d, want %d (6.1)", tree.Controls[c611Idx].ParentIdx, c61Idx)
	}

	// Verify Annex A domains.
	var domainCount int
	for _, c := range tree.Controls {
		if c.Kind == "domain" {
			domainCount++
		}
	}
	if domainCount != 4 {
		t.Errorf("domain count=%d, want 4 (A.5-A.8)", domainCount)
	}

	// Verify Annex A controls.
	var annexCount int
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			annexCount++
		}
	}
	if annexCount != 6 {
		t.Errorf("annex-control count=%d, want 6", annexCount)
	}

	// A.5.1 under A.5.
	var a5Idx, a51Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "A.5" {
			a5Idx = i
		}
		if c.Citation == "A.5.1" {
			a51Idx = i
		}
	}
	if a5Idx < 0 || a51Idx < 0 {
		t.Fatal("A.5 or A.5.1 not found")
	}
	if tree.Controls[a51Idx].ParentIdx != a5Idx {
		t.Errorf("A.5.1 parentIdx=%d, want %d (A.5)", tree.Controls[a51Idx].ParentIdx, a5Idx)
	}
	if tree.Controls[a51Idx].Title != "Annex A control A.5.1" {
		t.Errorf("A.5.1 title=%q, want 'Annex A control A.5.1'", tree.Controls[a51Idx].Title)
	}
	if tree.Controls[a51Idx].TitleOriginal != nil {
		t.Errorf("A.5.1 title_original=%v, want nil", tree.Controls[a51Idx].TitleOriginal)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic: %s ord=%d, %s ord=%d",
				tree.Controls[i-1].Citation, tree.Controls[i-1].Ordinal,
				tree.Controls[i].Citation, tree.Controls[i].Ordinal)
		}
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

func TestBuildISO27001Tree_Empty(t *testing.T) {
	empty := `{"pages":[]}`
	_, err := BuildISO27001Tree(json.RawMessage(empty), "iso27001", "2022")
	if err == nil {
		t.Fatal("expected error for empty capture")
	}
}

func TestBuildISO27001Tree_InvalidJSON(t *testing.T) {
	_, err := BuildISO27001Tree(json.RawMessage(`{bad}`), "iso27001", "2022")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// syntheticISO27002 is a minimal fixture for ISO 27002:2022.
const syntheticISO27002 = `{
  "pages": [
    {
      "n": 1,
      "text": "ISO/IEC 27002:2022(E)\nScope\n"
    },
    {
      "n": 19,
      "text": "5 Organizational controls\nThis clause contains organizational control measures.\n5.1 Invented policies for information security\nInvented implementation guidance for policies.\n5.2 Invented information security roles and responsibilities\nInvented guidance for roles.\n"
    },
    {
      "n": 50,
      "text": "6 People controls\nThis clause contains people control measures.\n6.1 Invented screening controls\nInvented guidance for screening.\n"
    },
    {
      "n": 100,
      "text": "7 Physical controls\nThis clause contains physical control measures.\n7.1 Invented physical security perimeters\nInvented guidance for perimeters.\n"
    },
    {
      "n": 130,
      "text": "8 Technological controls\nThis clause contains technological control measures.\n8.1 Invented user endpoint devices\nInvented guidance for endpoints.\n8.2 Invented privileged access rights\nInvented guidance for access.\n"
    }
  ]
}`

func TestBuildISOControlCatalogTree_27002_Synthetic(t *testing.T) {
	tree, err := BuildISOControlCatalogTree(json.RawMessage(syntheticISO27002), "iso27002", "2022")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	if tree.Title != "ISO/IEC 27002 2022" {
		t.Errorf("title=%q, want %q", tree.Title, "ISO/IEC 27002 2022")
	}

	// Expected: 4 domains + 6 controls = 10
	if len(tree.Controls) != 10 {
		t.Fatalf("controls=%d, want 10; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Domains.
	var domains []string
	for _, c := range tree.Controls {
		if c.Kind == "domain" {
			domains = append(domains, c.Citation)
		}
	}
	if len(domains) != 4 {
		t.Errorf("domains=%v, want 4", domains)
	}

	// Controls.
	var controls []string
	for _, c := range tree.Controls {
		if c.Kind == "control" {
			controls = append(controls, c.Citation)
		}
	}
	if len(controls) != 6 {
		t.Errorf("controls=%v, want 6", controls)
	}

	// 5.1 under theme 5.
	var theme5Idx, c51Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "5" {
			theme5Idx = i
		}
		if c.Citation == "5.1" {
			c51Idx = i
		}
	}
	if theme5Idx < 0 || c51Idx < 0 {
		t.Fatal("theme 5 or 5.1 not found")
	}
	if tree.Controls[c51Idx].ParentIdx != theme5Idx {
		t.Errorf("5.1 parentIdx=%d, want %d (theme 5)", tree.Controls[c51Idx].ParentIdx, theme5Idx)
	}
	if tree.Controls[c51Idx].Title != "Control 5.1" {
		t.Errorf("5.1 title=%q, want 'Control 5.1'", tree.Controls[c51Idx].Title)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic at %d", i)
		}
	}
}

// syntheticISO27017 is a minimal fixture for ISO 27017:2015.
const syntheticISO27017 = `{
  "pages": [
    {
      "n": 1,
      "text": "ISO/IEC 27017:2015(E)\nScope\n"
    },
    {
      "n": 16,
      "text": "5 Information security policies\n5.1 Management direction for information security\nThe objective specified in clause 5.1 applies.\n5.1.1 Invented policies for information security\nInvented implementation guidance for cloud policies.\n5.1.2 Invented review of the policies\nInvented guidance for cloud policy review.\n"
    },
    {
      "n": 20,
      "text": "6 Organization of information security\n6.1 Internal organization\nThe objective specified in clause 6.1 applies.\n6.1.1 Invented information security roles\nInvented cloud guidance for roles.\n"
    },
    {
      "n": 37,
      "text": "CLD.6.3 Invented relationship between CSC and CSP\nInvented cloud-specific section.\nCLD.6.3.1 Invented shared roles in cloud\nInvented cloud control for shared roles.\nCLD.8.1 Invented responsibility for assets\nInvented cloud-specific asset section.\nCLD.8.1.5 Invented removal of cloud customer assets\nInvented cloud control for asset removal.\n"
    }
  ]
}`

func TestBuildISOControlCatalogTree_27017_Synthetic(t *testing.T) {
	tree, err := BuildISOControlCatalogTree(json.RawMessage(syntheticISO27017), "iso27017", "2015")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	if tree.Title != "ISO/IEC 27017 2015" {
		t.Errorf("title=%q, want %q", tree.Title, "ISO/IEC 27017 2015")
	}

	// Expected: sections 5, 5.1, 5.1.1, 5.1.2, 6, 6.1, 6.1.1 = 7 numbered
	// + CLD.6.3, CLD.6.3.1, CLD.8.1, CLD.8.1.5 = 4 CLD
	// Total: 11
	if len(tree.Controls) != 11 {
		t.Fatalf("controls=%d, want 11; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// All controls are kind 'control'.
	for _, c := range tree.Controls {
		if c.Kind != "control" {
			t.Errorf("%s kind=%s, want control", c.Citation, c.Kind)
		}
	}

	// CLD.6.3.1 under CLD.6.3.
	var cld63Idx, cld631Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "CLD.6.3" {
			cld63Idx = i
		}
		if c.Citation == "CLD.6.3.1" {
			cld631Idx = i
		}
	}
	if cld63Idx < 0 || cld631Idx < 0 {
		t.Fatal("CLD.6.3 or CLD.6.3.1 not found")
	}
	if tree.Controls[cld631Idx].ParentIdx != cld63Idx {
		t.Errorf("CLD.6.3.1 parentIdx=%d, want %d (CLD.6.3)", tree.Controls[cld631Idx].ParentIdx, cld63Idx)
	}

	// 5.1.1 under 5.1.
	var s51Idx, s511Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "5.1" {
			s51Idx = i
		}
		if c.Citation == "5.1.1" {
			s511Idx = i
		}
	}
	if s51Idx < 0 || s511Idx < 0 {
		t.Fatal("5.1 or 5.1.1 not found")
	}
	if tree.Controls[s511Idx].ParentIdx != s51Idx {
		t.Errorf("5.1.1 parentIdx=%d, want %d (5.1)", tree.Controls[s511Idx].ParentIdx, s51Idx)
	}

	// Titles are neutral.
	for _, c := range tree.Controls {
		if c.TitleOriginal != nil {
			t.Errorf("%s title_original=%v, want nil", c.Citation, c.TitleOriginal)
		}
		want := "Control " + c.Citation
		if c.Title != want {
			t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}
}

// syntheticISO27018 is a minimal fixture for ISO 27018:2019.
const syntheticISO27018 = `{
  "pages": [
    {
      "n": 1,
      "text": "ISO/IEC 27018:2019(E)\nScope\n"
    },
    {
      "n": 10,
      "text": "5 Information security policies\n5.1 Management direction for information security\nThe objective applies.\n5.1.1 Invented policies for information security\nInvented PII guidance for policies.\n"
    },
    {
      "n": 25,
      "text": "A.2.1 Invented obligation to co-operate regarding PII\nInvented PII Annex control text.\nA.3.1 Invented public cloud PII processor purpose\nInvented PII processor control text.\nA.3.2 Invented commercial use restriction\nInvented restriction control text.\n"
    }
  ]
}`

func TestBuildISOControlCatalogTree_27018_Synthetic(t *testing.T) {
	tree, err := BuildISOControlCatalogTree(json.RawMessage(syntheticISO27018), "iso27018", "2019")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	if tree.Title != "ISO/IEC 27018 2019" {
		t.Errorf("title=%q, want %q", tree.Title, "ISO/IEC 27018 2019")
	}

	// Expected: sections 5, 5.1, 5.1.1 = 3 numbered
	// + Annex A.2.1, A.3.1, A.3.2 = 3 annex
	// Total: 6
	if len(tree.Controls) != 6 {
		t.Fatalf("controls=%d, want 6; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Count kinds.
	var controlCount, annexCount int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "control":
			controlCount++
		case "annex-control":
			annexCount++
		}
	}
	if controlCount != 3 {
		t.Errorf("control kind count=%d, want 3", controlCount)
	}
	if annexCount != 3 {
		t.Errorf("annex-control kind count=%d, want 3", annexCount)
	}

	// Annex controls have proper titles.
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			want := "Annex A control " + c.Citation
			if c.Title != want {
				t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
			}
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}
}

func TestBuildISOControlCatalogTree_UnsupportedFramework(t *testing.T) {
	fixture := `{"pages":[{"n":1,"text":"test"}]}`
	_, err := BuildISOControlCatalogTree(json.RawMessage(fixture), "unknown", "1.0")
	if err == nil {
		t.Fatal("expected error for unsupported framework")
	}
	if !strings.Contains(err.Error(), "unsupported framework") {
		t.Errorf("error=%v, expected mention of unsupported framework", err)
	}
}

func TestBuildISOControlCatalogTree_Empty(t *testing.T) {
	empty := `{"pages":[]}`
	_, err := BuildISOControlCatalogTree(json.RawMessage(empty), "iso27002", "2022")
	if err == nil {
		t.Fatal("expected error for empty capture")
	}
}

// --- Golden tests (require data files, skipped if absent) ---

func TestBuildISO27001Tree_Golden(t *testing.T) {
	const pdfPath = "../../data/iso/iso-iec-27001-2022.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildISO27001Tree(json.RawMessage(raw), "iso27001", "2022")
	if err != nil {
		t.Fatalf("BuildISO27001Tree: %v", err)
	}

	// --- Golden count pins ---

	// Count clauses (kind=clause).
	var clauseCount int
	for _, c := range tree.Controls {
		if c.Kind == "clause" {
			clauseCount++
		}
	}
	// Clauses 4-10 + subclauses. From survey: 41 total.
	if clauseCount != 41 {
		t.Errorf("clause count=%d, want 41", clauseCount)
	}

	// Annex A domains (A.5-A.8).
	var domainCount int
	for _, c := range tree.Controls {
		if c.Kind == "domain" {
			domainCount++
		}
	}
	if domainCount != 4 {
		t.Errorf("domain count=%d, want 4 (A.5-A.8)", domainCount)
	}

	// Annex A controls: exactly 93.
	var annexCount int
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			annexCount++
		}
	}
	if annexCount != 93 {
		t.Errorf("annex-control count=%d, want 93", annexCount)
	}

	// Total: 41 clauses + 4 domains + 93 controls = 138.
	totalControls := len(tree.Controls)
	if totalControls != 138 {
		t.Errorf("total controls=%d, want 138 (41 clauses + 4 domains + 93 annex)", totalControls)
	}

	// Annex A distribution by theme.
	themeCounts := map[string]int{}
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			parts := strings.Split(c.Citation, ".")
			if len(parts) >= 2 {
				themeCounts["A."+parts[1]]++
			}
		}
	}
	// Official 27001:2022 Annex A distribution.
	if themeCounts["A.5"] != 37 {
		t.Errorf("theme A.5 controls=%d, want 37", themeCounts["A.5"])
	}
	if themeCounts["A.6"] != 8 {
		t.Errorf("theme A.6 controls=%d, want 8", themeCounts["A.6"])
	}
	if themeCounts["A.7"] != 14 {
		t.Errorf("theme A.7 controls=%d, want 14", themeCounts["A.7"])
	}
	if themeCounts["A.8"] != 34 {
		t.Errorf("theme A.8 controls=%d, want 34", themeCounts["A.8"])
	}

	// Verify all Annex A controls parent to their domain.
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			if c.ParentIdx < 0 {
				t.Errorf("annex-control %s has no parent", c.Citation)
				continue
			}
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "domain" {
				t.Errorf("annex-control %s parent is %s (kind=%s), want domain", c.Citation, parent.Citation, parent.Kind)
			}
		}
	}

	// All controls have status=active.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("%s status=%s, want active", c.Citation, c.Status)
		}
	}

	// All controls have title_original=nil.
	for _, c := range tree.Controls {
		if c.TitleOriginal != nil {
			t.Errorf("%s title_original=%v, want nil", c.Citation, c.TitleOriginal)
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic: %s ord=%d, %s ord=%d",
				tree.Controls[i-1].Citation, tree.Controls[i-1].Ordinal,
				tree.Controls[i].Citation, tree.Controls[i].Ordinal)
		}
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

func TestBuildISOControlCatalogTree_27002_Golden(t *testing.T) {
	const pdfPath = "../../data/iso/iso-iec-27002-2022.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildISOControlCatalogTree(json.RawMessage(raw), "iso27002", "2022")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	// Domains: 4 (themes 5-8).
	var domainCount int
	for _, c := range tree.Controls {
		if c.Kind == "domain" {
			domainCount++
		}
	}
	if domainCount != 4 {
		t.Errorf("domain count=%d, want 4", domainCount)
	}

	// Controls: exactly 93.
	var controlCount int
	for _, c := range tree.Controls {
		if c.Kind == "control" {
			controlCount++
		}
	}
	if controlCount != 93 {
		t.Errorf("control count=%d, want 93", controlCount)
	}

	// Total: 4 + 93 = 97.
	if len(tree.Controls) != 97 {
		t.Errorf("total=%d, want 97", len(tree.Controls))
	}

	// Theme distribution.
	themeCounts := map[string]int{}
	for _, c := range tree.Controls {
		if c.Kind == "control" {
			parts := strings.Split(c.Citation, ".")
			themeCounts[parts[0]]++
		}
	}
	if themeCounts["5"] != 37 {
		t.Errorf("theme 5 controls=%d, want 37", themeCounts["5"])
	}
	if themeCounts["6"] != 8 {
		t.Errorf("theme 6 controls=%d, want 8", themeCounts["6"])
	}
	if themeCounts["7"] != 14 {
		t.Errorf("theme 7 controls=%d, want 14", themeCounts["7"])
	}
	if themeCounts["8"] != 34 {
		t.Errorf("theme 8 controls=%d, want 34", themeCounts["8"])
	}

	// All controls parent to their domain.
	for _, c := range tree.Controls {
		if c.Kind == "control" {
			if c.ParentIdx < 0 {
				t.Errorf("control %s has no parent", c.Citation)
				continue
			}
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "domain" {
				t.Errorf("control %s parent is %s (kind=%s), want domain", c.Citation, parent.Citation, parent.Kind)
			}
		}
	}

	// All title_original nil.
	for _, c := range tree.Controls {
		if c.TitleOriginal != nil {
			t.Errorf("%s title_original=%v, want nil", c.Citation, c.TitleOriginal)
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic at %d", i)
		}
	}
}

func TestBuildISOControlCatalogTree_27017_Golden(t *testing.T) {
	const pdfPath = "../../data/iso/iso-iec-27017-2015.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildISOControlCatalogTree(json.RawMessage(raw), "iso27017", "2015")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	// Count numbered sections and CLD items.
	var numberedCount, cldCount int
	for _, c := range tree.Controls {
		if strings.HasPrefix(c.Citation, "CLD.") {
			cldCount++
		} else {
			numberedCount++
		}
	}

	// 14 clause headings (5-18) + 35 objective sections + 114 subsections = 163.
	if numberedCount != 163 {
		t.Errorf("numbered sections=%d, want 163", numberedCount)
	}

	// CLD entries: 13 total (7 controls + 6 parent sections).
	if cldCount != 13 {
		t.Errorf("CLD count=%d, want 13", cldCount)
	}

	// Verify the 7 specific CLD controls.
	expectedCLD := map[string]bool{
		"CLD.6.3.1":  true,
		"CLD.8.1.5":  true,
		"CLD.9.5.1":  true,
		"CLD.9.5.2":  true,
		"CLD.12.1.5": true,
		"CLD.12.4.5": true,
		"CLD.13.1.4": true,
	}
	for _, c := range tree.Controls {
		if strings.HasPrefix(c.Citation, "CLD.") {
			parts := strings.Split(c.Citation, ".")
			if len(parts) == 4 {
				if !expectedCLD[c.Citation] {
					t.Errorf("unexpected CLD control: %s", c.Citation)
				}
			}
		}
	}

	// CLD parent hierarchy: CLD.6.3.1 -> CLD.6.3.
	var cld63Idx, cld631Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "CLD.6.3" {
			cld63Idx = i
		}
		if c.Citation == "CLD.6.3.1" {
			cld631Idx = i
		}
	}
	if cld63Idx >= 0 && cld631Idx >= 0 {
		if tree.Controls[cld631Idx].ParentIdx != cld63Idx {
			t.Errorf("CLD.6.3.1 parentIdx=%d, want %d (CLD.6.3)", tree.Controls[cld631Idx].ParentIdx, cld63Idx)
		}
	} else {
		t.Error("CLD.6.3 or CLD.6.3.1 not found")
	}

	// All kind='control'.
	for _, c := range tree.Controls {
		if c.Kind != "control" {
			t.Errorf("%s kind=%s, want control", c.Citation, c.Kind)
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic at %d: %s(%d) >= %s(%d)",
				i, tree.Controls[i-1].Citation, tree.Controls[i-1].Ordinal,
				tree.Controls[i].Citation, tree.Controls[i].Ordinal)
		}
	}
}

func TestBuildISOControlCatalogTree_27018_Golden(t *testing.T) {
	const pdfPath = "../../data/iso/iso-iec-27018-2019.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildISOControlCatalogTree(json.RawMessage(raw), "iso27018", "2019")
	if err != nil {
		t.Fatalf("BuildISOControlCatalogTree: %v", err)
	}

	// Count sections and Annex A.
	var sectionCount, annexCount int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "control":
			sectionCount++
		case "annex-control":
			annexCount++
		}
	}

	// 14 clause headings (5-18) + 81 subsections = 95.
	if sectionCount != 95 {
		t.Errorf("section count=%d, want 95", sectionCount)
	}

	// Annex A PII controls: exactly 25.
	if annexCount != 25 {
		t.Errorf("annex count=%d, want 25", annexCount)
	}

	// Verify specific Annex A controls.
	expectedAnnex := []string{
		"A.2.1", "A.3.1", "A.3.2", "A.5.1",
		"A.6.1", "A.6.2", "A.8.1",
		"A.10.1", "A.10.2", "A.10.3",
		"A.11.1", "A.11.2", "A.11.3", "A.11.4", "A.11.5",
		"A.11.6", "A.11.7", "A.11.8", "A.11.9", "A.11.10",
		"A.11.11", "A.11.12", "A.11.13",
		"A.12.1", "A.12.2",
	}
	found := map[string]bool{}
	for _, c := range tree.Controls {
		if c.Kind == "annex-control" {
			found[c.Citation] = true
		}
	}
	for _, want := range expectedAnnex {
		if !found[want] {
			t.Errorf("missing Annex A control: %s", want)
		}
	}

	// All controls have status=active.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("%s status=%s, want active", c.Citation, c.Status)
		}
	}

	// All title_original nil.
	for _, c := range tree.Controls {
		if c.TitleOriginal != nil {
			t.Errorf("%s title_original=%v, want nil", c.Citation, c.TitleOriginal)
		}
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic at %d", i)
		}
	}
}
