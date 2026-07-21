package normalize

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// syntheticPCI is a minimal pdf-pages-json fixture covering:
// - 1 Requirement header (Requirement 1)
// - X.Y group heading (1.1) with Customized Approach Objective
// - X.Y.Z leaf (1.1.1) with body text and Applicability Notes
// - X.Y.Z leaf (1.1.2) body only, no labeled sections
// - Another Requirement header (Requirement 4)
// - X.Y group (4.1) with Customized Approach Objective
// - X.Y.Z leaf (4.1.1)
// - X.Y.Z.W deeper leaf (4.1.1.1)
// - Appendix header (Appendix A1)
// - A1.1 appendix group
// - A1.1.1 appendix leaf
// - Testing procedure lines (.a, .b) that must be excluded
// - Page boundaries
//
// ALL WORDING IS INVENTED — no PCI DSS normative text.
const syntheticPCI = `{
  "pages": [
    {
      "n": 1,
      "text": "Cover page — invented header for test.\n"
    },
    {
      "n": 42,
      "text": "Requirement 1: Invented Firewall Safeguards\nOrganizations must protect their network.\n1.1 Processes and mechanisms for safeguarding networks are defined and understood.\nCustomized Approach Objective:\nThis requirement focuses on invented network protections.\nApplicability Notes:\nThis applies to all invented entities.\n"
    },
    {
      "n": 43,
      "text": "1.1.1 All security policies and operational procedures identified in Requirement 1 are managed.\nApplicability Notes:\nThis note applies to leaf 1.1.1.\n1.1.1.a Examine documented policies to verify.\n1.1.1.b Interview personnel to verify.\n1.1.2 Roles and responsibilities for performing activities are documented and understood.\n"
    },
    {
      "n": 100,
      "text": "Requirement 4: Invented Encryption Requirements\nProtect data in transit.\n4.1 Processes for protecting data are defined.\nCustomized Approach Objective:\nEncryption goals are met.\n"
    },
    {
      "n": 101,
      "text": "4.1.1 Strong encryption is used for data over public networks.\n4.1.1.1 Certificates used for encryption are confirmed valid.\n4.1.1.1.a Examine system configurations to verify.\n"
    },
    {
      "n": 300,
      "text": "Appendix A1: Invented Multi-Tenant Guidance\nAdditional requirements for providers.\nA1.1 Multi-tenant providers separate environments.\nA1.1.1 Logical separation is implemented as follows:\n"
    }
  ]
}`

func TestBuildPCITree_Synthetic(t *testing.T) {
	tree, err := BuildPCITree(json.RawMessage(syntheticPCI), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// Title.
	if tree.Title != "PCI DSS v4.0.1" {
		t.Errorf("title=%q, want %q", tree.Title, "PCI DSS v4.0.1")
	}

	// Expected controls:
	// Requirement 1 (root), 1.1 (group), 1.1.1 (leaf), 1.1.2 (leaf)
	// Requirement 4 (root), 4.1 (group), 4.1.1 (leaf), 4.1.1.1 (deeper)
	// A1 (appendix root), A1.1 (group), A1.1.1 (leaf)
	// Total: 11
	if len(tree.Controls) != 11 {
		t.Fatalf("controls=%d, want 11; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// --- Requirement 1 root ---
	req1 := tree.Controls[0]
	if req1.Citation != "1" {
		t.Errorf("control[0] citation=%s, want 1", req1.Citation)
	}
	if req1.CitationNorm != "1" {
		t.Errorf("control[0] citation_norm=%s, want 1", req1.CitationNorm)
	}
	if req1.Kind != "requirement" {
		t.Errorf("control[0] kind=%s, want requirement", req1.Kind)
	}
	if req1.Title != "Requirement 1" {
		t.Errorf("control[0] title=%q, want 'Requirement 1'", req1.Title)
	}
	if req1.TitleOriginal != nil {
		t.Errorf("control[0] title_original=%v, want nil", req1.TitleOriginal)
	}
	if req1.ParentIdx != -1 {
		t.Errorf("control[0] parentIdx=%d, want -1", req1.ParentIdx)
	}
	if req1.Status != "active" {
		t.Errorf("control[0] status=%s, want active", req1.Status)
	}
	// Root nodes have no body.
	if req1.Body != nil {
		t.Errorf("control[0] body should be nil for root, got %q", *req1.Body)
	}

	// --- 1.1 group ---
	c11 := tree.Controls[1]
	if c11.Citation != "1.1" {
		t.Errorf("control[1] citation=%s, want 1.1", c11.Citation)
	}
	if c11.Kind != "requirement" {
		t.Errorf("control[1] kind=%s, want requirement", c11.Kind)
	}
	if c11.Title != "Requirement 1.1" {
		t.Errorf("control[1] title=%q, want 'Requirement 1.1'", c11.Title)
	}
	if c11.ParentIdx != 0 {
		t.Errorf("control[1] parentIdx=%d, want 0 (Requirement 1)", c11.ParentIdx)
	}
	// 1.1 has body with Customized Approach Objective and Applicability Notes.
	if c11.Body == nil {
		t.Fatal("1.1 body is nil")
	}
	if !strings.Contains(*c11.Body, "Customized Approach Objective:") {
		t.Errorf("1.1 body missing Customized Approach Objective label")
	}
	if !strings.Contains(*c11.Body, "Applicability Notes:") {
		t.Errorf("1.1 body missing Applicability Notes label")
	}

	// --- 1.1.1 leaf ---
	c111 := tree.Controls[2]
	if c111.Citation != "1.1.1" {
		t.Errorf("control[2] citation=%s, want 1.1.1", c111.Citation)
	}
	if c111.ParentIdx != 1 {
		t.Errorf("control[2] parentIdx=%d, want 1 (1.1)", c111.ParentIdx)
	}
	if c111.Body == nil {
		t.Fatal("1.1.1 body is nil")
	}
	// Should have Applicability Notes section.
	if !strings.Contains(*c111.Body, "Applicability Notes:") {
		t.Errorf("1.1.1 body missing Applicability Notes label")
	}
	// Testing procedure lines (.a, .b) must NOT appear in body.
	if strings.Contains(*c111.Body, "1.1.1.a") || strings.Contains(*c111.Body, "1.1.1.b") {
		t.Errorf("1.1.1 body should not contain testing procedures")
	}

	// --- 1.1.2 leaf ---
	c112 := tree.Controls[3]
	if c112.Citation != "1.1.2" {
		t.Errorf("control[3] citation=%s, want 1.1.2", c112.Citation)
	}
	if c112.ParentIdx != 1 {
		t.Errorf("control[3] parentIdx=%d, want 1 (1.1)", c112.ParentIdx)
	}

	// --- Requirement 4 root ---
	req4 := tree.Controls[4]
	if req4.Citation != "4" {
		t.Errorf("control[4] citation=%s, want 4", req4.Citation)
	}
	if req4.ParentIdx != -1 {
		t.Errorf("control[4] parentIdx=%d, want -1", req4.ParentIdx)
	}

	// --- 4.1.1.1 deeper leaf ---
	c4111 := tree.Controls[7]
	if c4111.Citation != "4.1.1.1" {
		t.Errorf("control[7] citation=%s, want 4.1.1.1", c4111.Citation)
	}
	// parent = 4.1.1 (index 6)
	if c4111.ParentIdx != 6 {
		t.Errorf("control[7] parentIdx=%d, want 6 (4.1.1)", c4111.ParentIdx)
	}

	// --- Appendix A1 root ---
	a1 := tree.Controls[8]
	if a1.Citation != "A1" {
		t.Errorf("control[8] citation=%s, want A1", a1.Citation)
	}
	if a1.Kind != "requirement" {
		t.Errorf("control[8] kind=%s, want requirement", a1.Kind)
	}
	if a1.Title != "Requirement A1" {
		t.Errorf("control[8] title=%q, want 'Requirement A1'", a1.Title)
	}
	if a1.ParentIdx != -1 {
		t.Errorf("control[8] parentIdx=%d, want -1", a1.ParentIdx)
	}

	// --- A1.1 group ---
	a11 := tree.Controls[9]
	if a11.Citation != "A1.1" {
		t.Errorf("control[9] citation=%s, want A1.1", a11.Citation)
	}
	if a11.ParentIdx != 8 {
		t.Errorf("control[9] parentIdx=%d, want 8 (A1)", a11.ParentIdx)
	}

	// --- A1.1.1 leaf ---
	a111 := tree.Controls[10]
	if a111.Citation != "A1.1.1" {
		t.Errorf("control[10] citation=%s, want A1.1.1", a111.Citation)
	}
	if a111.ParentIdx != 9 {
		t.Errorf("control[10] parentIdx=%d, want 9 (A1.1)", a111.ParentIdx)
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

	// All controls are kind=requirement.
	for _, c := range tree.Controls {
		if c.Kind != "requirement" {
			t.Errorf("%s kind=%s, want requirement", c.Citation, c.Kind)
		}
	}

	// No mappings (PCI DSS parser emits none).
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

func TestBuildPCITree_EmptyCapture(t *testing.T) {
	empty := `{"pages":[]}`
	_, err := BuildPCITree(json.RawMessage(empty), "pcidss", "v4.0.1")
	if err == nil {
		t.Fatal("expected error for empty capture")
	}
	if !strings.Contains(err.Error(), "no requirement") {
		t.Errorf("error=%v, want mention of no requirements", err)
	}
}

func TestBuildPCITree_InvalidJSON(t *testing.T) {
	_, err := BuildPCITree(json.RawMessage(`{bad json}`), "pcidss", "v4.0.1")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildPCITree_TestProcExclusion(t *testing.T) {
	// Verify testing procedures are excluded from the tree.
	fixture := `{
  "pages": [
    {
      "n": 1,
      "text": "Requirement 1: Invented Safeguards\n1.1 Invented group.\n1.1.1 Invented leaf.\n1.1.1.a Invented testing procedure.\n1.1.1.b Another test step.\n"
    }
  ]
}`
	tree, err := BuildPCITree(json.RawMessage(fixture), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// Should have 3 controls: Req 1, 1.1, 1.1.1 — no .a or .b
	if len(tree.Controls) != 3 {
		t.Fatalf("controls=%d, want 3; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}
	for _, c := range tree.Controls {
		if strings.Contains(c.Citation, ".a") || strings.Contains(c.Citation, ".b") {
			t.Errorf("testing procedure %s should not be in tree", c.Citation)
		}
	}
}

func TestBuildPCITree_Depth5(t *testing.T) {
	// Verify depth-5 nesting (like 9.5.1.2.1).
	fixture := `{
  "pages": [
    {
      "n": 1,
      "text": "Requirement 9: Invented Physical Access\n9.5 Invented POI devices.\n9.5.1 Invented POI protections.\n9.5.1.2 Invented inspections.\n9.5.1.2.1 Invented inspection frequency.\n"
    }
  ]
}`
	tree, err := BuildPCITree(json.RawMessage(fixture), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// Req 9, 9.5, 9.5.1, 9.5.1.2, 9.5.1.2.1 = 5
	if len(tree.Controls) != 5 {
		t.Fatalf("controls=%d, want 5; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Verify parentage: 9.5.1.2.1 -> 9.5.1.2 -> 9.5.1 -> 9.5 -> 9
	c95121 := tree.Controls[4]
	if c95121.Citation != "9.5.1.2.1" {
		t.Errorf("control[4] citation=%s, want 9.5.1.2.1", c95121.Citation)
	}
	if c95121.ParentIdx != 3 { // 9.5.1.2 at index 3
		t.Errorf("9.5.1.2.1 parentIdx=%d, want 3 (9.5.1.2)", c95121.ParentIdx)
	}
}

func TestBuildPCITree_MidLineCollision(t *testing.T) {
	// Verify that a requirement ID appearing mid-line (due to go-fitz column
	// concatenation) is still captured. This reproduces the 10.2.1.4 pattern:
	// guidance text from a prior column runs into the requirement text on the
	// same line.
	fixture := `{
  "pages": [
    {
      "n": 1,
      "text": "Requirement 10: Invented Audit Logging\n10.2 Invented audit events.\n10.2.1 Invented audit details.\n10.2.1.3 Invented audit log access tracking.\n10.2.1.3 Invented testing procedure for audit log access.\n"
    },
    {
      "n": 2,
      "text": "Some guidance about attackers trying multiple times. 10.2.1.4 Invented audit log invalid access tracking.\n"
    },
    {
      "n": 3,
      "text": "Customized Approach Objective 10.2.1.4 Invented testing for invalid access.\n10.2.1.5 Invented audit log privilege changes.\n"
    }
  ]
}`
	tree, err := BuildPCITree(json.RawMessage(fixture), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// Expected: Req 10, 10.2, 10.2.1, 10.2.1.3, 10.2.1.4, 10.2.1.5 = 6
	if len(tree.Controls) != 6 {
		t.Fatalf("controls=%d, want 6; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Verify 10.2.1.4 is present with correct parent.
	var found1014 bool
	for _, c := range tree.Controls {
		if c.Citation == "10.2.1.4" {
			found1014 = true
			if c.Body == nil {
				t.Errorf("10.2.1.4 body is nil")
			}
			// Parent should be 10.2.1.
			parentIdx := c.ParentIdx
			if parentIdx < 0 || tree.Controls[parentIdx].Citation != "10.2.1" {
				t.Errorf("10.2.1.4 parent=%d (citation=%s), want 10.2.1",
					parentIdx, tree.Controls[parentIdx].Citation)
			}
		}
	}
	if !found1014 {
		t.Error("10.2.1.4 not found in tree — mid-line collision dropped it")
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

// TestBuildPCITree_BodyPurity asserts that no parsed PCI body contains
// stop-line markers — "Defined Approach Testing Procedures" or a line
// ending in standalone "Guidance". This converts the manual 2026-07-20
// audit (documented in docs/design/SCHEMA.md) into an automated invariant.
// Gated on data/ presence, same as the golden test.
func TestBuildPCITree_BodyPurity(t *testing.T) {
	const pdfPath = "../../data/pcissc/pci-dss-v4.0.1.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildPCITree(json.RawMessage(raw), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// rePurityGuidance matches a line ending in standalone "Guidance"
	// (the column header that signals non-requirement content).
	rePurityGuidance := regexp.MustCompile(`(?:^|\s)Guidance\s*$`)

	var violations []string
	for _, c := range tree.Controls {
		if c.Body == nil {
			continue
		}
		body := *c.Body
		if strings.Contains(body, "Defined Approach Testing Procedures") {
			violations = append(violations, c.Citation+": contains 'Defined Approach Testing Procedures'")
		}
		for _, line := range strings.Split(body, "\n") {
			if rePurityGuidance.MatchString(line) {
				violations = append(violations, c.Citation+": line matches standalone 'Guidance' header")
				break
			}
		}
	}
	if len(violations) > 0 {
		t.Errorf("%d bodies contain stop-line markers (expected 0):", len(violations))
		// Show at most 10 for readability.
		for i, v := range violations {
			if i >= 10 {
				t.Errorf("  ... and %d more", len(violations)-10)
				break
			}
			t.Errorf("  %s", v)
		}
	}
}

func TestBuildPCITree_Golden(t *testing.T) {
	const pdfPath = "../../data/pcissc/pci-dss-v4.0.1.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildPCITree(json.RawMessage(raw), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	// --- Golden count pins ---

	// Total controls: 15 roots (12 requirement headers + 3 appendix groups)
	// + 351 requirement IDs = 366.
	// 351 = 350 line-start IDs + 1 mid-line ID (10.2.1.4, recovered from
	// go-fitz column concatenation).
	totalControls := len(tree.Controls)
	if totalControls != 366 {
		t.Errorf("total controls=%d, want 366", totalControls)
	}

	// Count roots (parentIdx == -1).
	var roots int
	for _, c := range tree.Controls {
		if c.ParentIdx == -1 {
			roots++
		}
	}
	if roots != 15 {
		t.Errorf("roots=%d, want 15 (12 req headers + 3 appendix groups)", roots)
	}

	// Depth distribution: X.Y=71, X.Y.Z=230, X.Y.Z.W=49, depth-5=1.
	depthCounts := map[int]int{}
	for _, c := range tree.Controls {
		if c.ParentIdx == -1 {
			continue // skip roots
		}
		depth := strings.Count(c.Citation, ".") + 1
		depthCounts[depth]++
	}
	if depthCounts[2] != 71 {
		t.Errorf("X.Y depth=%d, want 71", depthCounts[2])
	}
	if depthCounts[3] != 230 {
		t.Errorf("X.Y.Z depth=%d, want 230", depthCounts[3])
	}
	if depthCounts[4] != 49 {
		t.Errorf("X.Y.Z.W depth=%d, want 49", depthCounts[4])
	}
	if depthCounts[5] != 1 {
		t.Errorf("depth-5=%d, want 1", depthCounts[5])
	}

	// All controls have kind=requirement.
	for _, c := range tree.Controls {
		if c.Kind != "requirement" {
			t.Errorf("%s kind=%s, want requirement", c.Citation, c.Kind)
		}
	}

	// All controls are active.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("%s status=%s, want active", c.Citation, c.Status)
		}
	}

	// All non-root controls have title = "Requirement <citation>".
	for _, c := range tree.Controls {
		want := "Requirement " + c.Citation
		if c.Title != want {
			t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
		}
	}

	// All controls have title_original = nil.
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

	// Non-root controls have non-nil body.
	var nilBodies int
	for _, c := range tree.Controls {
		if c.ParentIdx != -1 && c.Body == nil {
			nilBodies++
		}
	}
	if nilBodies > 0 {
		t.Errorf("%d non-root controls have nil body", nilBodies)
	}

	// Body length sanity: most bodies should have at least 20 chars.
	var shortBodies int
	for _, c := range tree.Controls {
		if c.Body != nil && len(*c.Body) < 20 {
			shortBodies++
		}
	}
	if shortBodies > 5 {
		t.Errorf("%d controls have body < 20 chars", shortBodies)
	}

	// Customized Approach Objective present in many bodies.
	var caoCount int
	for _, c := range tree.Controls {
		if c.Body != nil && strings.Contains(*c.Body, "Customized Approach Objective:") {
			caoCount++
		}
	}
	if caoCount < 100 {
		t.Errorf("controls with Customized Approach Objective: %d, want >= 100", caoCount)
	}

	// Applicability Notes present in some bodies.
	var anCount int
	for _, c := range tree.Controls {
		if c.Body != nil && strings.Contains(*c.Body, "Applicability Notes:") {
			anCount++
		}
	}
	if anCount < 50 {
		t.Errorf("controls with Applicability Notes: %d, want >= 50", anCount)
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}

	// Verify specific parentage: A3.2.5.1 under A3.2.5.
	var a325Idx, a3251Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "A3.2.5" {
			a325Idx = i
		}
		if c.Citation == "A3.2.5.1" {
			a3251Idx = i
		}
	}
	if a325Idx < 0 || a3251Idx < 0 {
		t.Fatal("A3.2.5 or A3.2.5.1 not found")
	}
	if tree.Controls[a3251Idx].ParentIdx != a325Idx {
		t.Errorf("A3.2.5.1 parentIdx=%d, want %d (A3.2.5)", tree.Controls[a3251Idx].ParentIdx, a325Idx)
	}

	// Verify depth-5: 9.5.1.2.1 under 9.5.1.2.
	var _9512Idx, _95121Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "9.5.1.2" {
			_9512Idx = i
		}
		if c.Citation == "9.5.1.2.1" {
			_95121Idx = i
		}
	}
	if _9512Idx < 0 || _95121Idx < 0 {
		t.Fatal("9.5.1.2 or 9.5.1.2.1 not found")
	}
	if tree.Controls[_95121Idx].ParentIdx != _9512Idx {
		t.Errorf("9.5.1.2.1 parentIdx=%d, want %d (9.5.1.2)", tree.Controls[_95121Idx].ParentIdx, _9512Idx)
	}
}

// TestBuildPCITree_GuidanceInBody verifies that body text containing the word
// "Guidance" mid-sentence (e.g. "Consult related Guidance") does NOT trigger
// the stop-line — only a standalone "Guidance" column-header line does.
func TestBuildPCITree_GuidanceInBody(t *testing.T) {
	fixture := `{
  "pages": [
    {
      "n": 1,
      "text": "Requirement 1: Invented Safeguards\n1.1 Invented group.\n1.1.1 Invented leaf requirement for fictional safeguards.\nConsult related Guidance documents for fictional supplemental details.\nAdditional invented requirement text here.\nGuidance\nThis is guidance column text that should be excluded.\n"
    }
  ]
}`
	tree, err := BuildPCITree(json.RawMessage(fixture), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	c111 := findByCitation(tree.Controls, "1.1.1")
	if c111 == nil {
		t.Fatal("1.1.1 not found")
	}
	if c111.Body == nil {
		t.Fatal("1.1.1 body is nil")
	}
	body := *c111.Body

	// Body MUST contain the mid-sentence "Guidance" line (not truncated).
	if !strings.Contains(body, "Consult related Guidance") {
		t.Error("body truncated at mid-sentence 'Guidance' — stop-line too broad")
	}

	// Body must NOT contain the standalone "Guidance" column header or
	// the guidance-column text after it.
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "Guidance" {
			t.Error("standalone 'Guidance' column header leaked into body")
		}
	}
	if strings.Contains(body, "guidance column text") {
		t.Error("guidance column content leaked into body")
	}
}

// TestBuildPCITree_ReqTestProcGuidanceHeader verifies the concatenated column
// header "Requirements and Testing Procedures Guidance" — the dominant stop-
// line variant in the real document — still triggers body truncation.
func TestBuildPCITree_ReqTestProcGuidanceHeader(t *testing.T) {
	fixture := `{
  "pages": [
    {
      "n": 1,
      "text": "Requirement 1: Invented Safeguards\n1.1 Invented group.\n1.1.1 Invented leaf for fictional safeguards.\nRequirements and Testing Procedures Guidance\nThis is non-requirement content.\n"
    }
  ]
}`
	tree, err := BuildPCITree(json.RawMessage(fixture), "pcidss", "v4.0.1")
	if err != nil {
		t.Fatalf("BuildPCITree: %v", err)
	}

	c111 := findByCitation(tree.Controls, "1.1.1")
	if c111 == nil {
		t.Fatal("1.1.1 not found")
	}
	if c111.Body != nil && strings.Contains(*c111.Body, "non-requirement") {
		t.Error("concatenated column header did not stop body collection")
	}
}
