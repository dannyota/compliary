package normalize

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// syntheticCCM is a minimal workbook-rows-json fixture covering:
// - preamble row 1 with JSON metadata, row 2 empty
// - header row 3
// - 2 domain headers (one with & in acronym)
// - 3 control rows with varying applicability
// - 1 trailing non-domain row (no ' - ' separator)
// - 1 copyright trailer row
//
// All wording is INVENTED — no CCM specification prose.
const syntheticCCM = `{
  "sheets": [
    {
      "name": "Introduction",
      "rows": [{"ref": "A1", "value": "intro text"}]
    },
    {
      "name": "CCM",
      "rows": [
        {"ref": "A1", "value": "{\"specification_name\":\"Cloud Controls Matrix\",\"specification_version\":\"4.1.0\"}"},
        {"ref": "A3", "value": "Control Domain"},
        {"ref": "B3", "value": "Control Title"},
        {"ref": "C3", "value": "Control ID"},
        {"ref": "D3", "value": "Control Specification"},
        {"ref": "E3", "value": "CCM Lite"},
        {"ref": "F3", "value": "IaaS"},
        {"ref": "G3", "value": "PaaS"},
        {"ref": "H3", "value": "SaaS"},
        {"ref": "I3", "value": "Phys"},
        {"ref": "J3", "value": "Network"},

        {"ref": "A4", "value": "Testing & Validation - T&V"},
        {"ref": "A5", "value": "Testing & Validation"},
        {"ref": "B5", "value": "Validation Procedures"},
        {"ref": "C5", "value": "T&V-01"},
        {"ref": "D5", "value": "Establish procedures for validation."},
        {"ref": "E5", "value": "No"},
        {"ref": "F5", "value": "Shared"},
        {"ref": "G5", "value": "Shared"},
        {"ref": "H5", "value": "Shared"},
        {"ref": "I5", "value": "True"},

        {"ref": "A6", "value": "Testing & Validation"},
        {"ref": "B6", "value": "Review Schedule"},
        {"ref": "C6", "value": "T&V-02"},
        {"ref": "D6", "value": "Define review schedules."},
        {"ref": "E6", "value": "Yes"},
        {"ref": "F6", "value": "CSP-Owned"},
        {"ref": "G6", "value": "CSP-Owned"},
        {"ref": "H6", "value": "CSC-Owned"},

        {"ref": "A7", "value": "Sample Domain - SD"},
        {"ref": "A8", "value": "Sample Domain"},
        {"ref": "B8", "value": "Basic Control"},
        {"ref": "C8", "value": "SD-01"},
        {"ref": "D8", "value": "Implement basic controls."},

        {"ref": "A9", "value": "End of Standard"},
        {"ref": "A10", "value": "Copyright notice text - All rights reserved."}
      ]
    },
    {
      "name": "Scope Applicability (Mappings)",
      "rows": [{"ref": "A1", "value": "This dataset is not available yet"}]
    }
  ]
}`

func TestBuildCCMTree_Synthetic(t *testing.T) {
	tree, err := BuildCCMTree(json.RawMessage(syntheticCCM), "csaccm", "v4.1")
	if err != nil {
		t.Fatalf("BuildCCMTree: %v", err)
	}

	// Title.
	if tree.Title != "CSA Cloud Controls Matrix (CCM) v4.1" {
		t.Errorf("title=%q, want %q", tree.Title, "CSA Cloud Controls Matrix (CCM) v4.1")
	}

	// Expected: 2 domains (T&V, SD) + 3 controls (T&V-01, T&V-02, SD-01) = 5.
	if len(tree.Controls) != 5 {
		t.Fatalf("controls=%d, want 5; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Domain T&V: kind=domain, citation=T&V, parent=-1.
	tv := tree.Controls[0]
	if tv.Kind != "domain" || tv.Citation != "T&V" {
		t.Errorf("control[0]: kind=%s citation=%s, want domain/T&V", tv.Kind, tv.Citation)
	}
	if tv.Title != "Testing & Validation" {
		t.Errorf("T&V title=%q, want 'Testing & Validation'", tv.Title)
	}
	if tv.ParentIdx != -1 {
		t.Errorf("T&V parentIdx=%d, want -1", tv.ParentIdx)
	}
	if tv.Status != "active" {
		t.Errorf("T&V status=%s, want active", tv.Status)
	}
	// Domains have no body.
	if tv.Body != nil {
		t.Errorf("T&V body should be nil, got %q", *tv.Body)
	}

	// Control T&V-01: parent = T&V (idx 0).
	tv01 := tree.Controls[1]
	if tv01.Kind != "control" || tv01.Citation != "T&V-01" {
		t.Errorf("control[1]: kind=%s citation=%s, want control/T&V-01", tv01.Kind, tv01.Citation)
	}
	if tv01.ParentIdx != 0 {
		t.Errorf("T&V-01 parentIdx=%d, want 0 (T&V)", tv01.ParentIdx)
	}
	if tv01.Title != "Validation Procedures" {
		t.Errorf("T&V-01 title=%q, want 'Validation Procedures'", tv01.Title)
	}
	if tv01.TitleOriginal == nil || *tv01.TitleOriginal != "Validation Procedures" {
		t.Errorf("T&V-01 title_original=%v, want 'Validation Procedures'", tv01.TitleOriginal)
	}

	// Body = specification + applicability lines.
	if tv01.Body == nil {
		t.Fatal("T&V-01 body is nil")
	}
	body01 := *tv01.Body
	if !strings.HasPrefix(body01, "Establish procedures for validation.") {
		t.Errorf("T&V-01 body should start with spec text, got: %q", body01[:50])
	}
	// Applicability lines.
	if !strings.Contains(body01, "IaaS: Shared") {
		t.Errorf("T&V-01 body missing 'IaaS: Shared', got: %q", body01)
	}
	if !strings.Contains(body01, "PaaS: Shared") {
		t.Errorf("T&V-01 body missing 'PaaS: Shared'")
	}
	if !strings.Contains(body01, "SaaS: Shared") {
		t.Errorf("T&V-01 body missing 'SaaS: Shared'")
	}
	if !strings.Contains(body01, "CCM Lite: No") {
		t.Errorf("T&V-01 body missing 'CCM Lite: No'")
	}
	// Phys is True (architectural relevance).
	if !strings.Contains(body01, "Phys: Yes") {
		t.Errorf("T&V-01 body missing 'Phys: Yes', got: %q", body01)
	}

	// T&V-02: CSP-Owned / CSC-Owned + CCM Lite Yes.
	tv02 := tree.Controls[2]
	if tv02.Body == nil {
		t.Fatal("T&V-02 body is nil")
	}
	body02 := *tv02.Body
	if !strings.Contains(body02, "IaaS: CSP-Owned") {
		t.Errorf("T&V-02 body missing 'IaaS: CSP-Owned'")
	}
	if !strings.Contains(body02, "SaaS: CSC-Owned") {
		t.Errorf("T&V-02 body missing 'SaaS: CSC-Owned'")
	}
	if !strings.Contains(body02, "CCM Lite: Yes") {
		t.Errorf("T&V-02 body missing 'CCM Lite: Yes'")
	}

	// SD-01: no applicability columns set — body should still have spec.
	sd01 := tree.Controls[4]
	if sd01.Citation != "SD-01" {
		t.Errorf("control[4] citation=%s, want SD-01", sd01.Citation)
	}
	if sd01.Body == nil {
		t.Fatal("SD-01 body is nil")
	}
	body_sd := *sd01.Body
	if !strings.HasPrefix(body_sd, "Implement basic controls.") {
		t.Errorf("SD-01 body should start with spec, got: %q", body_sd)
	}
	// No applicability lines (all empty).
	if strings.Contains(body_sd, "IaaS:") {
		t.Errorf("SD-01 body should have no IaaS line, got: %q", body_sd)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// No mappings (synthetic fixture has no mappings).
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

func TestBuildCCMTree_DomainHeaderParse(t *testing.T) {
	// Test various domain header formats including & in acronyms.
	tests := []struct {
		input    string
		wantName string
		wantAcr  string
		wantOK   bool
	}{
		{"Audit & Assurance - A&A", "Audit & Assurance", "A&A", true},
		{"Infrastructure Security - I&S", "Infrastructure Security", "I&S", true},
		{"Datacenter Security - DCS", "Datacenter Security", "DCS", true},
		{"Security Incident Management, E-Discovery, & Cloud Forensics - SEF", "Security Incident Management, E-Discovery, & Cloud Forensics", "SEF", true},
		{"End of Standard", "", "", false},
		{"Copyright notice - All rights reserved.", "", "", false},
	}
	for _, tc := range tests {
		name, acr, ok := parseDomainHeader(tc.input)
		if ok != tc.wantOK {
			t.Errorf("parseDomainHeader(%q): ok=%v, want %v", tc.input, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if name != tc.wantName {
			t.Errorf("parseDomainHeader(%q): name=%q, want %q", tc.input, name, tc.wantName)
		}
		if acr != tc.wantAcr {
			t.Errorf("parseDomainHeader(%q): acr=%q, want %q", tc.input, acr, tc.wantAcr)
		}
	}
}

func TestBuildCCMTree_MissingSheet(t *testing.T) {
	wb := `{"sheets":[{"name":"Other","rows":[]}]}`
	_, err := BuildCCMTree(json.RawMessage(wb), "csaccm", "v4.1")
	if err == nil {
		t.Fatal("expected error for missing CCM sheet")
	}
	if !strings.Contains(err.Error(), "CCM") {
		t.Errorf("error=%v, want mention of CCM", err)
	}
}

func TestBuildCCMTree_EmptyWorkbook(t *testing.T) {
	// Only header row, no data.
	wb := `{
  "sheets": [{"name": "CCM", "rows": [
    {"ref": "A3", "value": "Control Domain"},
    {"ref": "C3", "value": "Control ID"}
  ]}]
}`
	tree, err := BuildCCMTree(json.RawMessage(wb), "csaccm", "v4.1")
	if err != nil {
		t.Fatalf("BuildCCMTree: %v", err)
	}
	if len(tree.Controls) != 0 {
		t.Errorf("controls=%d, want 0", len(tree.Controls))
	}
}

func TestBuildCCMTree_MissingApplicability(t *testing.T) {
	// Control row with no applicability columns at all.
	wb := `{
  "sheets": [{"name": "CCM", "rows": [
    {"ref": "A3", "value": "Control Domain"},
    {"ref": "A4", "value": "Test Domain - TD"},
    {"ref": "B5", "value": "Some Control"},
    {"ref": "C5", "value": "TD-01"},
    {"ref": "D5", "value": "Specification text."}
  ]}]
}`
	tree, err := BuildCCMTree(json.RawMessage(wb), "csaccm", "v4.1")
	if err != nil {
		t.Fatalf("BuildCCMTree: %v", err)
	}
	if len(tree.Controls) != 2 {
		t.Fatalf("controls=%d, want 2", len(tree.Controls))
	}
	ctrl := tree.Controls[1]
	if ctrl.Body == nil {
		t.Fatal("body is nil")
	}
	// Body should be just the spec text (no applicability lines).
	if *ctrl.Body != "Specification text." {
		t.Errorf("body=%q, want 'Specification text.'", *ctrl.Body)
	}
}

func TestBuildCCMTree_Golden(t *testing.T) {
	const workbookPath = "../../data/csa/csa-ccm-v4.1.0.xlsx"
	if _, err := os.Stat(workbookPath); os.IsNotExist(err) {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	// Build capture at test time — never commit licensed captures.
	raw, err := extract.CaptureXLSXFile(workbookPath)
	if err != nil {
		t.Fatalf("CaptureXLSXFile: %v", err)
	}

	tree, err := BuildCCMTree(json.RawMessage(raw), "csaccm", "v4.1")
	if err != nil {
		t.Fatalf("BuildCCMTree: %v", err)
	}

	// Count by kind.
	var domains, controls int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "domain":
			domains++
		case "control":
			controls++
		default:
			t.Errorf("unexpected kind: %s for %s", c.Kind, c.Citation)
		}
	}

	// Golden count assertions (measured from survey).
	if domains != 17 {
		t.Errorf("domains=%d, want 17", domains)
	}
	if controls != 207 {
		t.Errorf("controls=%d, want 207", controls)
	}
	totalRows := domains + controls
	if totalRows != 224 {
		t.Errorf("total rows=%d, want 224 (17 domains + 207 controls)", totalRows)
	}

	// No withdrawn controls in CCM.
	var withdrawn int
	for _, c := range tree.Controls {
		if c.Status == "withdrawn" {
			withdrawn++
		}
	}
	if withdrawn != 0 {
		t.Errorf("withdrawn=%d, want 0", withdrawn)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Parentage: A&A-01 under domain A&A.
	var aaIdx, aa01Idx int = -1, -1
	for i, c := range tree.Controls {
		if c.Citation == "A&A" {
			aaIdx = i
		}
		if c.Citation == "A&A-01" {
			aa01Idx = i
		}
	}
	if aaIdx < 0 {
		t.Fatal("domain A&A not found")
	}
	if aa01Idx < 0 {
		t.Fatal("control A&A-01 not found")
	}
	if tree.Controls[aa01Idx].ParentIdx != aaIdx {
		t.Errorf("A&A-01 parentIdx=%d, want %d (A&A)", tree.Controls[aa01Idx].ParentIdx, aaIdx)
	}

	// A&A-01 applicability rendering: IaaS/PaaS/SaaS Shared, CCM Lite No.
	aa01 := tree.Controls[aa01Idx]
	if aa01.Body == nil {
		t.Fatal("A&A-01 body is nil")
	}
	body := *aa01.Body
	if !strings.Contains(body, "IaaS: Shared") {
		t.Errorf("A&A-01 body missing 'IaaS: Shared'")
	}
	if !strings.Contains(body, "PaaS: Shared") {
		t.Errorf("A&A-01 body missing 'PaaS: Shared'")
	}
	if !strings.Contains(body, "SaaS: Shared") {
		t.Errorf("A&A-01 body missing 'SaaS: Shared'")
	}
	if !strings.Contains(body, "CCM Lite: No") {
		t.Errorf("A&A-01 body missing 'CCM Lite: No'")
	}

	// All controls have bodies (specification text).
	for _, c := range tree.Controls {
		if c.Kind == "control" && c.Body == nil {
			t.Errorf("control %s has nil body", c.Citation)
		}
	}

	// All controls have title and title_original.
	for _, c := range tree.Controls {
		if c.Kind == "control" {
			if c.Title == "" {
				t.Errorf("control %s has empty title", c.Citation)
			}
			if c.TitleOriginal == nil {
				t.Errorf("control %s has nil title_original", c.Citation)
			}
		}
	}

	// No mapping edges (Mappings sheet says "not available yet").
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0 (Mappings sheet not available)", len(tree.Mappings))
	}

	// Every control's citation prefix matches its parent domain's citation.
	for _, c := range tree.Controls {
		if c.Kind != "control" {
			continue
		}
		dashIdx := strings.LastIndex(c.Citation, "-")
		if dashIdx < 0 {
			t.Errorf("control %s has no dash in citation", c.Citation)
			continue
		}
		prefix := c.Citation[:dashIdx]
		parent := tree.Controls[c.ParentIdx]
		if parent.Citation != prefix {
			t.Errorf("control %s: prefix=%s but parent citation=%s", c.Citation, prefix, parent.Citation)
		}
	}

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic: %s ord=%d, %s ord=%d",
				tree.Controls[i-1].Citation, tree.Controls[i-1].Ordinal,
				tree.Controls[i].Citation, tree.Controls[i].Ordinal)
		}
	}
}
