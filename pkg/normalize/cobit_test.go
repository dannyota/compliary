package normalize

import (
	"encoding/json"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// syntheticCOBIT is a minimal pdf-pages-json fixture covering:
// - 2 domain headers (Evaluate Direct and Monitor; Align Plan and Organize)
// - 2 objective headers (EDM01, APO01)
// - EDM01 with 2 practices (EDM01.01, EDM01.02)
// - APO01 with 1 practice (APO01.01)
// - Activities sub-lines that must NOT become separate controls
// - Practice description text split across a page boundary
// - RACI table duplicate practice ID that must be deduplicated
//
// ALL WORDING IS INVENTED — no ISACA normative text.
const syntheticCOBIT = `{
  "pages": [
    {
      "n": 1,
      "text": "Cover page — invented title for test.\n"
    },
    {
      "n": 29,
      "text": "CHAPTER 4 INVENTED GUIDANCE\n29\nEvaluate, Direct and Monitor\nDomain:  Evaluate, Direct and Monitor Governance Objective:  EDM01 — Invented Governance Safeguards Focus Area:  Invented Core Model\nDescription\nInvented description of the governance objective.\nPurpose\nInvented purpose of the governance objective.\nA. Component: Process\nGovernance Practice Example Metrics\nEDM01.01 Invented practice for evaluating controls. Identify stakeholders and evaluate current design.\na. Invented metric alpha b. Invented metric beta\nActivities Capability Level\n1. Invented activity analyzing factors.\n2\n2. Invented activity determining significance.\n3. Invented activity considering regulations.\n"
    },
    {
      "n": 30,
      "text": "INVENTED FRAMEWORK TITLE\n30\nEvaluate, Direct and Monitor\nA. Component: Process (cont.)\nRelated Guidance (Standards, Frameworks, Compliance Requirements) Detailed Reference\nInvented Standard 2018 Invented Reference\nGovernance Practice Example Metrics\nEDM01.02 Invented practice for directing controls. Guide the structures and processes for governance.\na. Invented metric gamma b. Invented metric delta\nActivities Capability Level\n1. Invented activity communicating principles.\n2\n2. Invented activity establishing structures.\nB. Component: Organizational Structures\nKey Governance Practice\nEDM01.01 Invented practice for evaluating controls. A R R R R\nEDM01.02 Invented practice for directing controls. A R R\n"
    },
    {
      "n": 55,
      "text": "CHAPTER 4 INVENTED GUIDANCE\n55\nAlign, Plan and Organize\nDomain:  Align, Plan and Organize Management Objective:  APO01 — Invented Management Framework Focus Area:  Invented Core Model\nDescription\nInvented description of the management objective.\nPurpose\nInvented purpose of the management objective.\nA. Component: Process\nManagement Practice Example Metrics\nAPO01.01 Invented practice for designing management system. Establish management components aligned with design.\na. Invented metric epsilon b. Invented metric zeta\nActivities Capability Level\n1. Invented activity evaluating components.\n2\n"
    }
  ]
}`

func TestBuildCOBITTree_Synthetic(t *testing.T) {
	tree, err := BuildCOBITTree(json.RawMessage(syntheticCOBIT), "cobit", "2019")
	if err != nil {
		t.Fatalf("BuildCOBITTree: %v", err)
	}

	// Title.
	if tree.Title != "COBIT 2019" {
		t.Errorf("title=%q, want %q", tree.Title, "COBIT 2019")
	}

	// Expected controls:
	// EDM domain root, APO domain root,
	// EDM01 objective, APO01 objective,
	// EDM01.01, EDM01.02 practices, APO01.01 practice
	// Total: 7
	if len(tree.Controls) != 7 {
		t.Fatalf("controls=%d, want 7; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// --- EDM domain root ---
	edm := tree.Controls[0]
	if edm.Citation != "EDM" {
		t.Errorf("control[0] citation=%s, want EDM", edm.Citation)
	}
	if edm.Kind != "domain" {
		t.Errorf("control[0] kind=%s, want domain", edm.Kind)
	}
	if edm.Title != "Evaluate, Direct and Monitor" {
		t.Errorf("control[0] title=%q, want 'Evaluate, Direct and Monitor'", edm.Title)
	}
	if edm.ParentIdx != -1 {
		t.Errorf("control[0] parentIdx=%d, want -1", edm.ParentIdx)
	}

	// --- EDM01 objective ---
	edm01 := tree.Controls[1]
	if edm01.Citation != "EDM01" {
		t.Errorf("control[1] citation=%s, want EDM01", edm01.Citation)
	}
	if edm01.Kind != "objective" {
		t.Errorf("control[1] kind=%s, want objective", edm01.Kind)
	}
	if edm01.Title != "Objective EDM01" {
		t.Errorf("control[1] title=%q, want 'Objective EDM01'", edm01.Title)
	}
	if edm01.TitleOriginal != nil {
		t.Errorf("control[1] title_original=%v, want nil", edm01.TitleOriginal)
	}
	if edm01.ParentIdx != 0 {
		t.Errorf("control[1] parentIdx=%d, want 0 (EDM)", edm01.ParentIdx)
	}

	// --- EDM01.01 practice ---
	p0101 := tree.Controls[2]
	if p0101.Citation != "EDM01.01" {
		t.Errorf("control[2] citation=%s, want EDM01.01", p0101.Citation)
	}
	if p0101.Kind != "practice" {
		t.Errorf("control[2] kind=%s, want practice", p0101.Kind)
	}
	if p0101.Title != "Practice EDM01.01" {
		t.Errorf("control[2] title=%q, want 'Practice EDM01.01'", p0101.Title)
	}
	if p0101.TitleOriginal != nil {
		t.Errorf("control[2] title_original=%v, want nil", p0101.TitleOriginal)
	}
	if p0101.ParentIdx != 1 {
		t.Errorf("control[2] parentIdx=%d, want 1 (EDM01)", p0101.ParentIdx)
	}
	// Body should be present (description text).
	if p0101.Body == nil {
		t.Fatal("EDM01.01 body is nil")
	}

	// --- EDM01.02 practice ---
	p0102 := tree.Controls[3]
	if p0102.Citation != "EDM01.02" {
		t.Errorf("control[3] citation=%s, want EDM01.02", p0102.Citation)
	}
	if p0102.Kind != "practice" {
		t.Errorf("control[3] kind=%s, want practice", p0102.Kind)
	}
	if p0102.ParentIdx != 1 {
		t.Errorf("control[3] parentIdx=%d, want 1 (EDM01)", p0102.ParentIdx)
	}

	// --- APO domain root ---
	apo := tree.Controls[4]
	if apo.Citation != "APO" {
		t.Errorf("control[4] citation=%s, want APO", apo.Citation)
	}
	if apo.Kind != "domain" {
		t.Errorf("control[4] kind=%s, want domain", apo.Kind)
	}
	if apo.ParentIdx != -1 {
		t.Errorf("control[4] parentIdx=%d, want -1", apo.ParentIdx)
	}

	// --- APO01 objective ---
	apo01 := tree.Controls[5]
	if apo01.Citation != "APO01" {
		t.Errorf("control[5] citation=%s, want APO01", apo01.Citation)
	}
	if apo01.Kind != "objective" {
		t.Errorf("control[5] kind=%s, want objective", apo01.Kind)
	}
	if apo01.ParentIdx != 4 {
		t.Errorf("control[5] parentIdx=%d, want 4 (APO)", apo01.ParentIdx)
	}

	// --- APO01.01 practice ---
	p010101 := tree.Controls[6]
	if p010101.Citation != "APO01.01" {
		t.Errorf("control[6] citation=%s, want APO01.01", p010101.Citation)
	}
	if p010101.Kind != "practice" {
		t.Errorf("control[6] kind=%s, want practice", p010101.Kind)
	}
	if p010101.ParentIdx != 5 {
		t.Errorf("control[6] parentIdx=%d, want 5 (APO01)", p010101.ParentIdx)
	}

	// All controls are active.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("%s status=%s, want active", c.Citation, c.Status)
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

	// No mappings (COBIT parser emits none).
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

// TestBuildCOBITTree_PageBoundary verifies practices whose description
// text spans a page boundary are captured correctly.
const syntheticCOBITPageBoundary = `{
  "pages": [
    {
      "n": 35,
      "text": "Evaluate, Direct and Monitor\nDomain:  Evaluate, Direct and Monitor Governance Objective:  EDM02 — Invented Benefits Delivery Focus Area:  Invented Core Model\nDescription\nInvented description.\nPurpose\nInvented purpose.\nA. Component: Process\nGovernance Practice Example Metrics\nEDM02.01 Invented target mix practice. Establish the target investment mix of invented types.\na. Invented metric alpha\nActivities Capability Level\n1. Invented activity one.\n2\n"
    },
    {
      "n": 36,
      "text": "INVENTED FRAMEWORK TITLE\n36\nEvaluate, Direct and Monitor\nA. Component: Process (cont.)\nGovernance Practice Example Metrics\nEDM02.02 Invented value evaluation practice. Evaluate the optimization of\na. Invented metric beta\n"
    },
    {
      "n": 37,
      "text": "Evaluate, Direct and Monitor\nA. Component: Process (cont.)\nGovernance Practice Example Metrics\nEDM02.03 Invented value direction practice.\na. Invented metric gamma\nEDM02.04 Invented value monitoring practice.\na. Invented metric delta\n"
    }
  ]
}`

func TestBuildCOBITTree_PageBoundary(t *testing.T) {
	tree, err := BuildCOBITTree(json.RawMessage(syntheticCOBITPageBoundary), "cobit", "2019")
	if err != nil {
		t.Fatalf("BuildCOBITTree: %v", err)
	}

	// Expected: EDM domain + EDM02 objective + 4 practices = 6
	if len(tree.Controls) != 6 {
		t.Fatalf("controls=%d, want 6; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Verify all 4 practices present.
	wantPractices := []string{"EDM02.01", "EDM02.02", "EDM02.03", "EDM02.04"}
	for _, wp := range wantPractices {
		found := false
		for _, c := range tree.Controls {
			if c.Citation == wp {
				found = true
				if c.Kind != "practice" {
					t.Errorf("%s kind=%s, want practice", wp, c.Kind)
				}
				break
			}
		}
		if !found {
			t.Errorf("practice %s not found", wp)
		}
	}

	// EDM02.03 and EDM02.04 on same page — both captured.
	var p03, p04 bool
	for _, c := range tree.Controls {
		if c.Citation == "EDM02.03" {
			p03 = true
		}
		if c.Citation == "EDM02.04" {
			p04 = true
		}
	}
	if !p03 || !p04 {
		t.Error("practices on same page not both captured")
	}
}

// TestBuildCOBITTree_PageBoundaryBodyContinuation verifies that a practice
// description spanning a PDF page boundary collects its continuation text
// from the next page. Before the fix, body collection stopped at the page's
// lines and the continuation was lost.
func TestBuildCOBITTree_PageBoundaryBodyContinuation(t *testing.T) {
	fixture := `{
  "pages": [
    {
      "n": 35,
      "text": "Evaluate, Direct and Monitor\nDomain:  Evaluate, Direct and Monitor Governance Objective:  EDM02 — Invented Benefits Focus Area:  Invented Core Model\nDescription\nInvented description.\nPurpose\nInvented purpose.\nA. Component: Process\nGovernance Practice Example Metrics\nEDM02.01 Invented practice beginning. The description starts here and continues\na. Invented metric alpha\n"
    },
    {
      "n": 36,
      "text": "on the next page with additional invented detail about the practice.\nActivities Capability Level\n1. Invented activity one.\n2\n"
    }
  ]
}`
	tree, err := BuildCOBITTree(json.RawMessage(fixture), "cobit", "2019")
	if err != nil {
		t.Fatalf("BuildCOBITTree: %v", err)
	}

	// Find EDM02.01.
	var edm0201 *ControlRow
	for i := range tree.Controls {
		if tree.Controls[i].Citation == "EDM02.01" {
			edm0201 = &tree.Controls[i]
			break
		}
	}
	if edm0201 == nil {
		t.Fatal("EDM02.01 not found")
	}
	if edm0201.Body == nil {
		t.Fatal("EDM02.01 body is nil")
	}

	body := *edm0201.Body

	// Body must contain the continuation text from page 36.
	if !strings.Contains(body, "additional invented detail") {
		t.Error("practice body lost page-boundary continuation text")
	}

	// Body must NOT contain Activities or activity lines (boundary markers).
	if strings.Contains(body, "Invented activity") {
		t.Error("Activities section leaked into body")
	}
}

func TestBuildCOBITTree_EmptyCapture(t *testing.T) {
	empty := `{"pages":[]}`
	_, err := BuildCOBITTree(json.RawMessage(empty), "cobit", "2019")
	if err == nil {
		t.Fatal("expected error for empty capture")
	}
	if !strings.Contains(err.Error(), "no objective") {
		t.Errorf("error=%v, want mention of no objectives", err)
	}
}

func TestBuildCOBITTree_InvalidJSON(t *testing.T) {
	_, err := BuildCOBITTree(json.RawMessage(`{bad json}`), "cobit", "2019")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildCOBITTree_Golden(t *testing.T) {
	const pdfPath = "../../data/isaca/cobit-2019-framework-governance-and-management-objectives.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildCOBITTree(json.RawMessage(raw), "cobit", "2019")
	if err != nil {
		t.Fatalf("BuildCOBITTree: %v", err)
	}

	// --- Golden count pins ---

	// Count by kind.
	kindCounts := map[string]int{}
	for _, c := range tree.Controls {
		kindCounts[c.Kind]++
	}

	// 5 domain roots.
	if kindCounts["domain"] != 5 {
		t.Errorf("domains=%d, want 5", kindCounts["domain"])
	}

	// 40 objectives.
	if kindCounts["objective"] != 40 {
		t.Errorf("objectives=%d, want 40", kindCounts["objective"])
	}

	// Per-domain objective counts: EDM=5, APO=14, BAI=11, DSS=6, MEA=4.
	domainObjCount := map[string]int{}
	for _, c := range tree.Controls {
		if c.Kind == "objective" {
			domainObjCount[c.Citation[:3]]++
		}
	}
	wantDomainObj := map[string]int{"EDM": 5, "APO": 14, "BAI": 11, "DSS": 6, "MEA": 4}
	for d, want := range wantDomainObj {
		if domainObjCount[d] != want {
			t.Errorf("%s objectives=%d, want %d", d, domainObjCount[d], want)
		}
	}

	// 231 practices (reconciled from go-fitz survey: 203 pdftotext upper
	// bound was an undercount due to table-cell rendering; go-fitz first-
	// occurrence within objective sections yields 231).
	if kindCounts["practice"] != 231 {
		t.Errorf("practices=%d, want 231", kindCounts["practice"])
	}

	// Per-domain practice counts.
	domainPracCount := map[string]int{}
	for _, c := range tree.Controls {
		if c.Kind == "practice" {
			domainPracCount[c.Citation[:3]]++
		}
	}
	wantDomainPrac := map[string]int{"EDM": 16, "APO": 83, "BAI": 72, "DSS": 38, "MEA": 22}
	for d, want := range wantDomainPrac {
		if domainPracCount[d] != want {
			t.Errorf("%s practices=%d, want %d", d, domainPracCount[d], want)
		}
	}

	// Total controls: 5 + 40 + 231 = 276.
	totalControls := len(tree.Controls)
	if totalControls != 276 {
		t.Errorf("total controls=%d, want 276", totalControls)
	}

	// All domain roots are parentIdx -1.
	for _, c := range tree.Controls {
		if c.Kind == "domain" && c.ParentIdx != -1 {
			t.Errorf("domain %s parentIdx=%d, want -1", c.Citation, c.ParentIdx)
		}
	}

	// All objectives parent to a domain.
	for _, c := range tree.Controls {
		if c.Kind == "objective" {
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "domain" {
				t.Errorf("objective %s parent kind=%s, want domain", c.Citation, parent.Kind)
			}
		}
	}

	// All practices parent to an objective.
	for _, c := range tree.Controls {
		if c.Kind == "practice" {
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "objective" {
				t.Errorf("practice %s parent kind=%s, want objective", c.Citation, parent.Kind)
			}
			// Practice's objective code matches its parent.
			if c.Citation[:5] != parent.Citation {
				t.Errorf("practice %s parent=%s, want %s", c.Citation, parent.Citation, c.Citation[:5])
			}
		}
	}

	// All controls are active.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("%s status=%s, want active", c.Citation, c.Status)
		}
	}

	// Title = neutral formula per kind.
	for _, c := range tree.Controls {
		switch c.Kind {
		case "practice":
			want := "Practice " + c.Citation
			if c.Title != want {
				t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
			}
		case "objective":
			want := "Objective " + c.Citation
			if c.Title != want {
				t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
			}
		}
	}

	// All controls have title_original = nil (licensed titling decision).
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

	// Practices have non-nil body (description text from the PDF).
	var nilBodies int
	for _, c := range tree.Controls {
		if c.Kind == "practice" && c.Body == nil {
			nilBodies++
		}
	}
	if nilBodies > 0 {
		t.Errorf("%d practices have nil body", nilBodies)
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}
