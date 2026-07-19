package normalize

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// syntheticCSF is a minimal workbook-rows-json fixture covering:
// - 2 function rows for one function (GV) to test dedupe
// - 1 active category (GV.OC)
// - 1 active subcategory with examples (GV.OC-01)
// - 1 withdrawn category (ID.BE) with incorporated-into
// - 1 withdrawn subcategory (ID.AM-06) with multi-target incorporated-into
// - 1 function row for ID (with description)
// - 1 withdrawn subcategory using "Moved into" spelling (ID.RM-03)
// - 1 withdrawn category with free-text target ("other Categories and Functions")
const syntheticCSF = `{
  "sheets": [
    {
      "name": "Introduction",
      "rows": [{"ref": "A1", "value": "intro text"}]
    },
    {
      "name": "CSF 2.0",
      "rows": [
        {"ref": "A2", "value": "Function"},
        {"ref": "B2", "value": "Category"},
        {"ref": "C2", "value": "Subcategory"},
        {"ref": "D2", "value": "Implementation Examples"},
        {"ref": "E2", "value": "Informative References"},

        {"ref": "A3", "value": "GOVERN (GV): The org cybersecurity strategy"},
        {"ref": "B4", "value": "Organizational Context (GV.OC): The circumstances and context"},
        {"ref": "C5", "value": "GV.OC-01: The organizational mission is understood"},
        {"ref": "D5", "value": "Ex1: Share the mission\nEx2: Identify stakeholders"},

        {"ref": "A6", "value": "IDENTIFY (ID): Current risks are understood"},
        {"ref": "B7", "value": "Asset Management (ID.AM): Assets are identified"},
        {"ref": "C8", "value": "ID.AM-06: [Withdrawn: Incorporated into GV.RR-02, GV.SC-02]"},
        {"ref": "B9", "value": "Business Environment (ID.BE): [Withdrawn: Incorporated into GV.OC]"},
        {"ref": "C10", "value": "ID.BE-01: [Withdrawn: Incorporated into GV.OC-05]"},
        {"ref": "C11", "value": "ID.RM-03: [Withdrawn: Moved into GV.RM-02]"},
        {"ref": "B12", "value": "Information Protection (PR.IP): [Withdrawn: Incorporated into other Categories and Functions]"},

        {"ref": "A13", "value": "GOVERN (GV)"},
        {"ref": "A14", "value": "IDENTIFY (ID)"}
      ]
    }
  ]
}`

func TestBuildCSFTree_Synthetic(t *testing.T) {
	tree, err := BuildCSFTree(json.RawMessage(syntheticCSF), "nistcsf", "2.0")
	if err != nil {
		t.Fatalf("BuildCSFTree: %v", err)
	}

	// Title.
	if tree.Title != "NIST Cybersecurity Framework (CSF) 2.0" {
		t.Errorf("title=%q, want %q", tree.Title, "NIST Cybersecurity Framework (CSF) 2.0")
	}

	// Expected rows: 2 functions (GV, ID deduped) + 4 categories (GV.OC, ID.AM, ID.BE, PR.IP)
	// + 4 subcategories (GV.OC-01, ID.AM-06, ID.BE-01, ID.RM-03) = 10.
	// Order follows sheet-row order: GV, GV.OC, GV.OC-01, ID, ID.AM, ID.AM-06,
	// ID.BE, ID.BE-01, ID.RM-03, PR.IP.
	if len(tree.Controls) != 10 {
		t.Fatalf("controls=%d, want 10; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// Verify function dedupe: GV should appear once with description.
	gv := tree.Controls[0]
	if gv.Kind != "function" || gv.Citation != "GV" {
		t.Errorf("first control: kind=%s citation=%s, want function/GV", gv.Kind, gv.Citation)
	}
	if gv.Title != "The org cybersecurity strategy" {
		t.Errorf("GV title=%q, want description from the described row", gv.Title)
	}
	if gv.ParentIdx != -1 {
		t.Errorf("GV parentIdx=%d, want -1", gv.ParentIdx)
	}
	if gv.Status != "active" {
		t.Errorf("GV status=%s, want active", gv.Status)
	}

	// Active category: GV.OC (idx 1).
	gvoc := tree.Controls[1]
	if gvoc.Kind != "category" || gvoc.Citation != "GV.OC" || gvoc.Status != "active" {
		t.Errorf("GV.OC: kind=%s citation=%s status=%s", gvoc.Kind, gvoc.Citation, gvoc.Status)
	}
	if gvoc.ParentIdx != 0 {
		t.Errorf("GV.OC parentIdx=%d, want 0 (GV)", gvoc.ParentIdx)
	}

	// Active subcategory: GV.OC-01 with examples in body (idx 2).
	gvoc01 := tree.Controls[2]
	if gvoc01.Kind != "subcategory" || gvoc01.Citation != "GV.OC-01" || gvoc01.Status != "active" {
		t.Errorf("GV.OC-01: kind=%s citation=%s status=%s", gvoc01.Kind, gvoc01.Citation, gvoc01.Status)
	}
	if gvoc01.ParentIdx != 1 {
		t.Errorf("GV.OC-01 parentIdx=%d, want 1 (GV.OC)", gvoc01.ParentIdx)
	}
	if gvoc01.Body == nil {
		t.Fatal("GV.OC-01 body is nil")
	}
	if !strings.Contains(*gvoc01.Body, "Ex1:") {
		t.Errorf("GV.OC-01 body missing Ex1: %s", *gvoc01.Body)
	}
	if !strings.Contains(*gvoc01.Body, "Ex2:") {
		t.Errorf("GV.OC-01 body missing Ex2: %s", *gvoc01.Body)
	}
	// Body should start with the statement, then blank line, then examples.
	if !strings.Contains(*gvoc01.Body, "The organizational mission is understood\n\nEx1:") {
		t.Errorf("GV.OC-01 body format wrong: %q", *gvoc01.Body)
	}

	// title_original should match title.
	if gvoc01.TitleOriginal == nil || *gvoc01.TitleOriginal != gvoc01.Title {
		t.Errorf("GV.OC-01 title_original=%v, want %q", gvoc01.TitleOriginal, gvoc01.Title)
	}

	// ID function (idx 3).
	id := tree.Controls[3]
	if id.Kind != "function" || id.Citation != "ID" {
		t.Errorf("control[3]: kind=%s citation=%s, want function/ID", id.Kind, id.Citation)
	}
	if id.Title != "Current risks are understood" {
		t.Errorf("ID title=%q, want description", id.Title)
	}

	// ID.AM active category (idx 4).
	idam := tree.Controls[4]
	if idam.Kind != "category" || idam.Citation != "ID.AM" || idam.Status != "active" {
		t.Errorf("ID.AM: kind=%s citation=%s status=%s", idam.Kind, idam.Citation, idam.Status)
	}
	if idam.ParentIdx != 3 {
		t.Errorf("ID.AM parentIdx=%d, want 3 (ID)", idam.ParentIdx)
	}

	// Withdrawn subcategory: ID.AM-06 with multi-target (idx 5).
	idam06 := tree.Controls[5]
	if idam06.Kind != "subcategory" || idam06.Citation != "ID.AM-06" || idam06.Status != "withdrawn" {
		t.Errorf("ID.AM-06: kind=%s citation=%s status=%s", idam06.Kind, idam06.Citation, idam06.Status)
	}
	if idam06.ParentIdx != 4 {
		t.Errorf("ID.AM-06 parentIdx=%d, want 4 (ID.AM)", idam06.ParentIdx)
	}
	// Withdrawn subcategory should have no body.
	if idam06.Body != nil {
		t.Errorf("ID.AM-06 body should be nil for withdrawn, got %q", *idam06.Body)
	}

	// Withdrawn category: ID.BE (idx 6).
	idbe := tree.Controls[6]
	if idbe.Kind != "category" || idbe.Citation != "ID.BE" || idbe.Status != "withdrawn" {
		t.Errorf("ID.BE: kind=%s citation=%s status=%s", idbe.Kind, idbe.Citation, idbe.Status)
	}
	// Title for withdrawn category should be the name part.
	if idbe.Title != "Business Environment" {
		t.Errorf("ID.BE title=%q, want 'Business Environment'", idbe.Title)
	}

	// Withdrawn subcategory under withdrawn category: ID.BE-01 (idx 7).
	idbe01 := tree.Controls[7]
	if idbe01.Kind != "subcategory" || idbe01.Citation != "ID.BE-01" || idbe01.Status != "withdrawn" {
		t.Errorf("ID.BE-01: kind=%s citation=%s status=%s", idbe01.Kind, idbe01.Citation, idbe01.Status)
	}
	// ID.BE-01 should parent to ID.BE (idx 6).
	if idbe01.ParentIdx != 6 {
		t.Errorf("ID.BE-01 parentIdx=%d, want 6 (ID.BE)", idbe01.ParentIdx)
	}

	// "Moved into" treated as "moved-to": ID.RM-03 (idx 8).
	idrm03 := tree.Controls[8]
	if idrm03.Kind != "subcategory" || idrm03.Citation != "ID.RM-03" || idrm03.Status != "withdrawn" {
		t.Errorf("ID.RM-03: kind=%s citation=%s status=%s", idrm03.Kind, idrm03.Citation, idrm03.Status)
	}

	// Withdrawn category with free-text target: PR.IP (idx 9).
	prip := tree.Controls[9]
	if prip.Kind != "category" || prip.Citation != "PR.IP" || prip.Status != "withdrawn" {
		t.Errorf("PR.IP: kind=%s citation=%s status=%s", prip.Kind, prip.Citation, prip.Status)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// --- Mapping edges ---
	// Expected edges:
	// ID.AM-06 → incorporated-into → GV.RR-02, GV.SC-02 (2 edges)
	// ID.BE → incorporated-into → GV.OC (1 edge)
	// ID.BE-01 → incorporated-into → GV.OC-05 (1 edge)
	// ID.RM-03 → moved-to → GV.RM-02 (1 edge)
	// PR.IP → incorporated-into → (free-text "other Categories and Functions") → 0 edges
	// Total: 5 edges
	if len(tree.Mappings) != 5 {
		t.Fatalf("mappings=%d, want 5", len(tree.Mappings))
	}

	// Check ID.AM-06 edges.
	m0 := tree.Mappings[0]
	if tree.Controls[m0.FromIdx].Citation != "ID.AM-06" {
		t.Errorf("m0 from=%s, want ID.AM-06", tree.Controls[m0.FromIdx].Citation)
	}
	if m0.Relationship != "incorporated-into" {
		t.Errorf("m0 relationship=%s, want incorporated-into", m0.Relationship)
	}
	if m0.ToCitationNorm != "GV.RR-02" {
		t.Errorf("m0 to=%s, want GV.RR-02", m0.ToCitationNorm)
	}
	if m0.ProvenanceDetail != "CSF 2.0!C8" {
		t.Errorf("m0 provenance=%s, want CSF 2.0!C8", m0.ProvenanceDetail)
	}

	m1 := tree.Mappings[1]
	if m1.ToCitationNorm != "GV.SC-02" {
		t.Errorf("m1 to=%s, want GV.SC-02", m1.ToCitationNorm)
	}

	// Check ID.BE edge to category-level target.
	m2 := tree.Mappings[2]
	if tree.Controls[m2.FromIdx].Citation != "ID.BE" {
		t.Errorf("m2 from=%s, want ID.BE", tree.Controls[m2.FromIdx].Citation)
	}
	if m2.ToCitationNorm != "GV.OC" {
		t.Errorf("m2 to=%s, want GV.OC", m2.ToCitationNorm)
	}

	// Check ID.RM-03 "Moved into" → moved-to.
	m4 := tree.Mappings[4]
	if tree.Controls[m4.FromIdx].Citation != "ID.RM-03" {
		t.Errorf("m4 from=%s, want ID.RM-03", tree.Controls[m4.FromIdx].Citation)
	}
	if m4.Relationship != "moved-to" {
		t.Errorf("m4 relationship=%s, want moved-to", m4.Relationship)
	}
}

// syntheticCSFWithRefs is a fixture that adds col E informative references to
// exercise the reference edge parser: prefix lookup, ISO edition stripping,
// PCI NULL-version, out-of-registry skip, 800-53 dedupe (5.1.1 + 5.2.0 → r5),
// ISO empty/None skip, multi-cite skip.
const syntheticCSFWithRefs = `{
  "sheets": [
    {
      "name": "CSF 2.0",
      "rows": [
        {"ref": "A2", "value": "Function"},
        {"ref": "B2", "value": "Category"},
        {"ref": "C2", "value": "Subcategory"},
        {"ref": "D2", "value": "Implementation Examples"},
        {"ref": "E2", "value": "Informative References"},

        {"ref": "A3", "value": "GOVERN (GV): The org cybersecurity strategy"},
        {"ref": "E3", "value": "SP 800-53 Rev 5.2.0: AC-01\nSP 800-53 Rev 5.1.1: AC-01\nISO/IEC 27001:2022: Annex A Controls: 5.1\nPCI DSS: 1.1.1\nCSF v1.1: GV\nCIS Controls v8.1: 1.1\nCIS Controls v8.0: 1.1\nCCMv4.0: AIS-01\nNICE Framework: T0001"},

        {"ref": "B4", "value": "Organizational Context (GV.OC): The circumstances"},
        {"ref": "E4", "value": "ISO/IEC 27001:2022: Mandatory Clause: None\nISO/IEC 27001:2022: Annex A Controls:\nISO/IEC 27001:2022: Mandatory Clause: 4.1\nISO/IEC 27001:2022: Control 5.1\nISO/IEC 27001:2022: Mandatory Clause: 7.1, 7.2"},

        {"ref": "C5", "value": "GV.OC-01: The organizational mission is understood"},
        {"ref": "E5", "value": "SP 800-53 Rev 5.2.0: AC-02\nSP 800-53 Rev 5.1.1: AC-02\nSP 800-53 Rev 5.2.0: AC-03"},

        {"ref": "A6", "value": "GOVERN (GV)"},
        {"ref": "A7", "value": "IDENTIFY (ID): Current risks are understood"}
      ]
    }
  ]
}`

var testRefSources = []ReferenceSource{
	{Prefix: "SP 800-53 Rev 5.2.0", ToFrameworkCode: "nist80053", ToVersionLabel: strPtr("r5"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "SP 800-53 Rev 5.1.1", ToFrameworkCode: "nist80053", ToVersionLabel: strPtr("r5"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "CCMv4.0", ToFrameworkCode: "csaccm", ToVersionLabel: strPtr("v4.0"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "ISO/IEC 27001", ToFrameworkCode: "iso27001", ToVersionLabel: strPtr("2022"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "PCI DSS", ToFrameworkCode: "pcidss", ToVersionLabel: nil, MappingSourceCode: "publisher-catalog"},
	{Prefix: "CSF v1.1", ToFrameworkCode: "nistcsf", ToVersionLabel: strPtr("1.1"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "CIS Controls v8.1", ToFrameworkCode: "ciscontrols", ToVersionLabel: strPtr("v8.1"), MappingSourceCode: "publisher-catalog"},
	{Prefix: "CIS Controls v8.0", ToFrameworkCode: "ciscontrols", ToVersionLabel: strPtr("v8"), MappingSourceCode: "publisher-catalog"},
}

func TestBuildCSFTree_References(t *testing.T) {
	tree, err := BuildCSFTree(json.RawMessage(syntheticCSFWithRefs), "nistcsf", "2.0", testRefSources...)
	if err != nil {
		t.Fatalf("BuildCSFTree: %v", err)
	}

	// Expect 3 controls: GV (function), GV.OC (category), GV.OC-01 (subcategory), ID (function).
	if len(tree.Controls) != 4 {
		t.Fatalf("controls=%d, want 4; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// --- Row 3 (GV function) references ---
	// SP 800-53 Rev 5.2.0: AC-01 + SP 800-53 Rev 5.1.1: AC-01 → DEDUPE to 1 edge (nist80053/r5/AC-01)
	// ISO/IEC 27001:2022: Annex A Controls: 5.1 → 1 edge
	// PCI DSS: 1.1.1 → 1 edge (NULL version)
	// CSF v1.1: GV → 1 edge
	// CIS Controls v8.1: 1.1 → 1 edge
	// CIS Controls v8.0: 1.1 → 1 edge
	// CCMv4.0: AIS-01 → 1 edge
	// NICE Framework: T0001 → skip (unknown prefix)
	// Total for GV: 7 edges

	// --- Row 4 (GV.OC category) references ---
	// ISO/IEC 27001:2022: Mandatory Clause: None → skip (None)
	// ISO/IEC 27001:2022: Annex A Controls: (bare) → skip (empty)
	// ISO/IEC 27001:2022: Mandatory Clause: 4.1 → 1 edge
	// ISO/IEC 27001:2022: Control 5.1 → 1 edge
	// ISO/IEC 27001:2022: Mandatory Clause: 7.1, 7.2 → skip (multi-cite)
	// Total for GV.OC: 2 edges

	// --- Row 5 (GV.OC-01 subcategory) references ---
	// SP 800-53 Rev 5.2.0: AC-02 + SP 800-53 Rev 5.1.1: AC-02 → DEDUPE to 1 edge (AC-02)
	// SP 800-53 Rev 5.2.0: AC-03 → 1 edge (only in 5.2.0)
	// Total for GV.OC-01: 2 edges

	// Grand total: 7 + 2 + 2 = 11 reference edges (no withdrawal edges in this fixture).
	if len(tree.Mappings) != 11 {
		t.Fatalf("mappings=%d, want 11", len(tree.Mappings))
	}

	// All reference edges should have relationship "related".
	for i, m := range tree.Mappings {
		if m.Relationship != "related" {
			t.Errorf("mapping[%d] relationship=%s, want related", i, m.Relationship)
		}
	}

	// Check the deduped SP 800-53 edge on GV.
	m0 := tree.Mappings[0]
	if m0.ToFrameworkCode != "nist80053" {
		t.Errorf("m0 to_fw=%s, want nist80053", m0.ToFrameworkCode)
	}
	if m0.ToCitationNorm != "AC-01" {
		t.Errorf("m0 to_cite=%s, want AC-01", m0.ToCitationNorm)
	}
	// Provenance should contain both release strings, sorted.
	if !strings.Contains(m0.ProvenanceDetail, "SP 800-53 Rev 5.1.1") ||
		!strings.Contains(m0.ProvenanceDetail, "SP 800-53 Rev 5.2.0") {
		t.Errorf("m0 provenance=%s, want both release strings", m0.ProvenanceDetail)
	}

	// Check PCI DSS edge (NULL version).
	var pciEdge *MappingEdge
	for i := range tree.Mappings {
		if tree.Mappings[i].ToFrameworkCode == "pcidss" {
			pciEdge = &tree.Mappings[i]
			break
		}
	}
	if pciEdge == nil {
		t.Fatal("no PCI DSS edge found")
	}
	if pciEdge.ToVersionLabel != nil {
		t.Errorf("PCI edge version=%v, want nil", pciEdge.ToVersionLabel)
	}
	if pciEdge.ToCitationNorm != "1.1.1" {
		t.Errorf("PCI edge cite=%s, want 1.1.1", pciEdge.ToCitationNorm)
	}

	// Check unknown prefix skip counts.
	if tree.RefSkips == nil {
		t.Fatal("RefSkips is nil")
	}
	if tree.RefSkips.UnknownPfx["NICE Framework"] != 1 {
		t.Errorf("NICE Framework skip=%d, want 1", tree.RefSkips.UnknownPfx["NICE Framework"])
	}
	// Check known prefix skips.
	isoSkips := tree.RefSkips.PerPrefix["ISO/IEC 27001"]
	if isoSkips != 3 {
		t.Errorf("ISO/IEC 27001 skips=%d, want 3 (None + bare + multi-cite)", isoSkips)
	}
}

func TestParseISOCitation(t *testing.T) {
	tests := []struct {
		rest     string
		want     string
		wantSkip bool
	}{
		{"2022: Annex A Controls: 5.1", "5.1", false},
		{"2022: Mandatory Clause: 8.1", "8.1", false},
		{"2022: Control 5.8", "5.8", false},
		{"2022: Control  8.6", "8.6", false},            // double space
		{"2022: Mandatory Clause: None", "", true},      // skip
		{"2022: Annex A Controls:", "", true},           // bare
		{"2022: Mandatory Clause: 7.1, 7.2", "", true},  // multi-cite
		{"2022: Mandatory Clause:  6.1,", "6.1", false}, // trailing comma stripped
		{"2022: Mandatory Clause:  4.1", "4.1", false},  // leading space in cite
		{"2013: Annex A Controls: A.5.1", "", true},     // wrong edition
		{"2022: Annex A Controls: None", "", true},      // None via Annex
	}
	for _, tc := range tests {
		got, skip := parseISOCitation(tc.rest)
		if skip != tc.wantSkip {
			t.Errorf("parseISOCitation(%q): skip=%v, want %v", tc.rest, skip, tc.wantSkip)
		}
		if !skip && got != tc.want {
			t.Errorf("parseISOCitation(%q): got=%q, want=%q", tc.rest, got, tc.want)
		}
	}
}

func TestBuildCSFTree_MissingSheet(t *testing.T) {
	wb := `{"sheets":[{"name":"Other","rows":[]}]}`
	_, err := BuildCSFTree(json.RawMessage(wb), "nistcsf", "2.0")
	if err == nil {
		t.Fatal("expected error for missing CSF 2.0 sheet")
	}
	if !strings.Contains(err.Error(), "CSF 2.0") {
		t.Errorf("error=%v, want mention of CSF 2.0", err)
	}
}

func TestBuildCSFTree_NoExamples(t *testing.T) {
	// Subcategory with no examples column.
	wb := `{
  "sheets": [{"name": "CSF 2.0", "rows": [
    {"ref": "A2", "value": "Function"},
    {"ref": "A3", "value": "GOVERN (GV): Description"},
    {"ref": "B4", "value": "Organizational Context (GV.OC): Statement here"},
    {"ref": "C5", "value": "GV.OC-01: Mission is understood"}
  ]}]
}`
	tree, err := BuildCSFTree(json.RawMessage(wb), "nistcsf", "2.0")
	if err != nil {
		t.Fatalf("BuildCSFTree: %v", err)
	}

	// Subcategory should have body = statement only (no examples).
	sub := tree.Controls[2]
	if sub.Kind != "subcategory" {
		t.Fatalf("expected subcategory, got %s", sub.Kind)
	}
	if sub.Body == nil {
		t.Fatal("subcategory body is nil")
	}
	if *sub.Body != "Mission is understood" {
		t.Errorf("body=%q, want 'Mission is understood'", *sub.Body)
	}
}

func TestBuildCSFTree_Golden(t *testing.T) {
	const workbookPath = "../../data/nist/nist-csf-2.0.xlsx"
	if _, err := os.Stat(workbookPath); os.IsNotExist(err) {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	// We don't re-extract the xlsx; read the capture from the DB export.
	// For golden tests, use the DB-captured workbook-rows-json directly.
	// Export via psql and feed to the builder.
	capturePath := "../../testdata/csf-2.0-capture.json"
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Skipf("golden capture file absent: %v", err)
	}

	tree, err := BuildCSFTree(json.RawMessage(raw), "nistcsf", "2.0")
	if err != nil {
		t.Fatalf("BuildCSFTree: %v", err)
	}

	// Count by kind.
	var functions, categories, subcategories int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "function":
			functions++
		case "category":
			categories++
		case "subcategory":
			subcategories++
		}
	}

	// Golden count assertions.
	if functions != 6 {
		t.Errorf("functions=%d, want 6", functions)
	}
	if categories != 34 {
		t.Errorf("categories=%d, want 34 (22 active + 12 withdrawn)", categories)
	}
	if subcategories != 185 {
		t.Errorf("subcategories=%d, want 185 (106 active + 79 withdrawn)", subcategories)
	}
	totalRows := functions + categories + subcategories
	if totalRows != 225 {
		t.Errorf("total rows=%d, want 225", totalRows)
	}

	// Withdrawn count.
	var withdrawn int
	for _, c := range tree.Controls {
		if c.Status == "withdrawn" {
			withdrawn++
		}
	}
	if withdrawn != 91 {
		t.Errorf("withdrawn=%d, want 91 (12 categories + 79 subcategories)", withdrawn)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Parentage: withdrawn subcategories under their withdrawn category when present.
	// ID.BE-01 should parent to ID.BE.
	idbeIdx := -1
	idbe01Idx := -1
	for i, c := range tree.Controls {
		if c.CitationNorm == "ID.BE" {
			idbeIdx = i
		}
		if c.CitationNorm == "ID.BE-01" {
			idbe01Idx = i
		}
	}
	if idbeIdx < 0 || idbe01Idx < 0 {
		t.Fatal("ID.BE or ID.BE-01 not found")
	}
	if tree.Controls[idbe01Idx].ParentIdx != idbeIdx {
		t.Errorf("ID.BE-01 parentIdx=%d, want %d (ID.BE)", tree.Controls[idbe01Idx].ParentIdx, idbeIdx)
	}

	// Examples present on active subcategory bodies.
	var exampleCount int
	for _, c := range tree.Controls {
		if c.Kind == "subcategory" && c.Status == "active" && c.Body != nil {
			if strings.Contains(*c.Body, "Ex1:") {
				exampleCount++
			}
		}
	}
	if exampleCount < 100 {
		t.Errorf("active subcategories with Ex1: %d, want >= 100", exampleCount)
	}

	// title_original set for all controls.
	for _, c := range tree.Controls {
		if c.Title != "" && c.TitleOriginal == nil {
			t.Errorf("title_original nil for %s", c.Citation)
		}
	}

	// Mapping edge counts pinned (withdrawal edges only, no ref sources).
	if len(tree.Mappings) != 136 {
		t.Errorf("mappings=%d, want 136", len(tree.Mappings))
	}
	var incInto, movedTo int
	for _, m := range tree.Mappings {
		switch m.Relationship {
		case "incorporated-into":
			incInto++
		case "moved-to":
			movedTo++
		}
	}
	if incInto != 117 {
		t.Errorf("incorporated-into=%d, want 117", incInto)
	}
	if movedTo != 19 {
		t.Errorf("moved-to=%d, want 19", movedTo)
	}
}

func TestBuildCSFTree_GoldenRefs(t *testing.T) {
	const workbookPath = "../../data/nist/nist-csf-2.0.xlsx"
	if _, err := os.Stat(workbookPath); os.IsNotExist(err) {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	capturePath := "../../testdata/csf-2.0-capture.json"
	raw, err := os.ReadFile(capturePath)
	if err != nil {
		t.Skipf("golden capture file absent: %v", err)
	}

	tree, err := BuildCSFTree(json.RawMessage(raw), "nistcsf", "2.0", testRefSources...)
	if err != nil {
		t.Fatalf("BuildCSFTree: %v", err)
	}

	// Count edges by target framework.
	type targetKey struct {
		fw  string
		ver string
	}
	edgesByTarget := make(map[targetKey]int)
	var refEdges int
	for _, m := range tree.Mappings {
		if m.Relationship == "related" {
			refEdges++
			ver := ""
			if m.ToVersionLabel != nil {
				ver = *m.ToVersionLabel
			}
			edgesByTarget[targetKey{m.ToFrameworkCode, ver}]++
		}
	}

	// Withdrawal edges unchanged: 136.
	withdrawalEdges := len(tree.Mappings) - refEdges
	if withdrawalEdges != 136 {
		t.Errorf("withdrawal edges=%d, want 136", withdrawalEdges)
	}

	// Per-target golden edge counts.
	// These are the EXACT counts from the parser reconciled against raw line counts.
	wantEdges := map[targetKey]int{
		{"nist80053", "r5"}:     747, // 1486 raw - 0 skips - 739 dedupes
		{"csaccm", "v4.0"}:      657, // 657 raw, no skips, no dedupes
		{"iso27001", "2022"}:    470, // 585 raw - 79 skips - 36 dedupes
		{"pcidss", ""}:          551, // 551 raw, no skips
		{"nistcsf", "1.1"}:      185, // 185 raw, no skips
		{"ciscontrols", "v8.1"}: 62,  // 62 raw, no skips
		{"ciscontrols", "v8"}:   60,  // 60 raw, no skips
	}

	for tk, want := range wantEdges {
		got := edgesByTarget[tk]
		ver := tk.ver
		if ver == "" {
			ver = "NULL"
		}
		if got != want {
			t.Errorf("edges to %s/%s: got=%d, want=%d", tk.fw, ver, got, want)
		}
	}

	totalRefEdges := 0
	for _, n := range wantEdges {
		totalRefEdges += n
	}
	if refEdges != totalRefEdges {
		t.Errorf("total ref edges=%d, want %d", refEdges, totalRefEdges)
	}

	// Skip counts.
	if tree.RefSkips == nil {
		t.Fatal("RefSkips is nil")
	}
	// ISO skips: 79 (66 MC:None + 9 Annex:None + 3 bare Annex + 1 multi-cite)
	if tree.RefSkips.PerPrefix["ISO/IEC 27001"] != 79 {
		t.Errorf("ISO/IEC 27001 skips=%d, want 79", tree.RefSkips.PerPrefix["ISO/IEC 27001"])
	}
	// No skips for other registered prefixes.
	for _, pfx := range []string{"SP 800-53 Rev 5.2.0", "SP 800-53 Rev 5.1.1", "CCMv4.0", "PCI DSS", "CSF v1.1", "CIS Controls v8.1", "CIS Controls v8.0"} {
		if s := tree.RefSkips.PerPrefix[pfx]; s != 0 {
			t.Errorf("%s skips=%d, want 0", pfx, s)
		}
	}
	// Unknown prefix counts (top ones).
	if tree.RefSkips.UnknownPfx["NICE Framework"] != 850 {
		t.Errorf("NICE Framework unknown=%d, want 850", tree.RefSkips.UnknownPfx["NICE Framework"])
	}
	if tree.RefSkips.UnknownPfx["SCF"] != 473 {
		t.Errorf("SCF unknown=%d, want 473", tree.RefSkips.UnknownPfx["SCF"])
	}
}

func controlIDs(controls []ControlRow) []string {
	ids := make([]string, len(controls))
	for i, c := range controls {
		ids[i] = c.Citation
	}
	return ids
}
