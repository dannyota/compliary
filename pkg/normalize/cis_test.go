package normalize

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// syntheticCIS is a minimal workbook-rows-json fixture covering:
// - 2 controls with safeguards (including float-string discipline: 4.1 AND 4.10)
// - IG column variations (all three, partial, none)
// - Asset Class and Security Function attributes
// - Control header rows with trailing NBSP (U+00A0)
const syntheticCIS = `{
  "sheets": [
    {
      "name": "Introduction",
      "rows": [{"ref": "A1", "value": "intro text"}]
    },
    {
      "name": "Controls v8.1.2",
      "rows": [
        {"ref": "A1", "value": "CIS Control"},
        {"ref": "B1", "value": "CIS Safeguard"},
        {"ref": "C1", "value": "Asset Class"},
        {"ref": "D1", "value": "Security Function"},
        {"ref": "E1", "value": "Title"},
        {"ref": "F1", "value": "Description"},
        {"ref": "G1", "value": "IG1"},
        {"ref": "H1", "value": "IG2"},
        {"ref": "I1", "value": "IG3"},

        {"ref": "A2", "value": "1 "},
        {"ref": "E2", "value": "Asset Inventory and Governance"},
        {"ref": "F2", "value": "Manage all enterprise assets actively."},

        {"ref": "A3", "value": "1"},
        {"ref": "B3", "value": "1.1"},
        {"ref": "C3", "value": "Devices"},
        {"ref": "D3", "value": "Identify"},
        {"ref": "E3", "value": "Maintain Detailed Asset List"},
        {"ref": "F3", "value": "Keep an accurate asset list updated."},
        {"ref": "G3", "value": "x"},
        {"ref": "H3", "value": "x"},
        {"ref": "I3", "value": "x"},

        {"ref": "A4", "value": "1"},
        {"ref": "B4", "value": "1.2"},
        {"ref": "C4", "value": "Devices"},
        {"ref": "D4", "value": "Respond"},
        {"ref": "E4", "value": "Handle Rogue Assets"},
        {"ref": "F4", "value": "Remove or quarantine rogue assets."},
        {"ref": "H4", "value": "x"},
        {"ref": "I4", "value": "x"},

        {"ref": "A5", "value": "4"},
        {"ref": "E5", "value": "Hardening Configuration"},
        {"ref": "F5", "value": "Establish secure configurations."},

        {"ref": "A6", "value": "4"},
        {"ref": "B6", "value": "4.1"},
        {"ref": "C6", "value": "Software"},
        {"ref": "D6", "value": "Protect"},
        {"ref": "E6", "value": "Use Secure Configurations"},
        {"ref": "F6", "value": "Apply hardened configurations."},
        {"ref": "G6", "value": "x"},
        {"ref": "H6", "value": "x"},
        {"ref": "I6", "value": "x"},

        {"ref": "A7", "value": "4"},
        {"ref": "B7", "value": "4.10"},
        {"ref": "C7", "value": "Network"},
        {"ref": "D7", "value": "Detect"},
        {"ref": "E7", "value": "Enforce Firewall Rules"},
        {"ref": "F7", "value": "Enforce firewall on mobile endpoints."},
        {"ref": "I7", "value": "x"},

        {"ref": "A8", "value": "4"},
        {"ref": "B8", "value": "4.2"},
        {"ref": "E8", "value": "Track Config Changes"},
        {"ref": "F8", "value": "Track changes to configurations."}
      ]
    }
  ]
}`

func TestBuildCISTree_Synthetic(t *testing.T) {
	tree, err := BuildCISTree(json.RawMessage(syntheticCIS), "ciscontrols", "v8.1")
	if err != nil {
		t.Fatalf("BuildCISTree: %v", err)
	}

	// Title.
	if tree.Title != "CIS Controls v8.1" {
		t.Errorf("title=%q, want %q", tree.Title, "CIS Controls v8.1")
	}

	// Expected: 2 controls + 5 safeguards = 7 rows.
	if len(tree.Controls) != 7 {
		t.Fatalf("controls=%d, want 7; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}

	// --- Control 1 ---
	c1 := tree.Controls[0]
	if c1.Kind != "control" {
		t.Errorf("c1 kind=%s, want control", c1.Kind)
	}
	if c1.Citation != "1" {
		t.Errorf("c1 citation=%s, want 1", c1.Citation)
	}
	if c1.CitationNorm != "1" {
		t.Errorf("c1 citation_norm=%s, want 1", c1.CitationNorm)
	}
	if c1.Title != "Asset Inventory and Governance" {
		t.Errorf("c1 title=%q", c1.Title)
	}
	if c1.Body == nil || *c1.Body != "Manage all enterprise assets actively." {
		t.Errorf("c1 body=%v", c1.Body)
	}
	if c1.ParentIdx != -1 {
		t.Errorf("c1 parentIdx=%d, want -1", c1.ParentIdx)
	}
	if c1.Status != "active" {
		t.Errorf("c1 status=%s, want active", c1.Status)
	}

	// --- Safeguard 1.1 ---
	s11 := tree.Controls[1]
	if s11.Kind != "safeguard" {
		t.Errorf("s1.1 kind=%s, want safeguard", s11.Kind)
	}
	if s11.Citation != "1.1" {
		t.Errorf("s1.1 citation=%s, want 1.1", s11.Citation)
	}
	if s11.ParentIdx != 0 {
		t.Errorf("s1.1 parentIdx=%d, want 0 (control 1)", s11.ParentIdx)
	}
	if s11.Title != "Maintain Detailed Asset List" {
		t.Errorf("s1.1 title=%q", s11.Title)
	}
	// Body = description + blank line + attributes.
	if s11.Body == nil {
		t.Fatal("s1.1 body is nil")
	}
	body11 := *s11.Body
	if !strings.Contains(body11, "Keep an accurate asset list updated.") {
		t.Errorf("s1.1 body missing description: %q", body11)
	}
	// Attribute lines.
	if !strings.Contains(body11, "Asset Class: Devices") {
		t.Errorf("s1.1 body missing Asset Class: %q", body11)
	}
	if !strings.Contains(body11, "Security Function: Identify") {
		t.Errorf("s1.1 body missing Security Function: %q", body11)
	}
	if !strings.Contains(body11, "Implementation Groups: IG1, IG2, IG3") {
		t.Errorf("s1.1 body missing IG line: %q", body11)
	}
	// Body format: description, blank line, then attributes.
	if !strings.Contains(body11, "Keep an accurate asset list updated.\n\nAsset Class:") {
		t.Errorf("s1.1 body format wrong: %q", body11)
	}

	// --- Safeguard 1.2 (partial IGs: IG2 and IG3 only) ---
	s12 := tree.Controls[2]
	if s12.Citation != "1.2" {
		t.Errorf("s1.2 citation=%s", s12.Citation)
	}
	if s12.Body == nil {
		t.Fatal("s1.2 body is nil")
	}
	if !strings.Contains(*s12.Body, "Implementation Groups: IG2, IG3") {
		t.Errorf("s1.2 body IG line wrong: %q", *s12.Body)
	}
	// IG1 should NOT be present.
	if strings.Contains(*s12.Body, "IG1") {
		t.Errorf("s1.2 body should not contain IG1: %q", *s12.Body)
	}

	// --- Float-string discipline: 4.1 vs 4.10 ---
	c4 := tree.Controls[3]
	if c4.Citation != "4" || c4.Kind != "control" {
		t.Errorf("c4: citation=%s kind=%s", c4.Citation, c4.Kind)
	}

	s41 := tree.Controls[4]
	if s41.Citation != "4.1" {
		t.Errorf("s4.1 citation=%s, want 4.1", s41.Citation)
	}
	if s41.ParentIdx != 3 {
		t.Errorf("s4.1 parentIdx=%d, want 3 (control 4)", s41.ParentIdx)
	}

	s410 := tree.Controls[5]
	if s410.Citation != "4.10" {
		t.Errorf("s4.10 citation=%s, want 4.10", s410.Citation)
	}
	if s410.ParentIdx != 3 {
		t.Errorf("s4.10 parentIdx=%d, want 3 (control 4)", s410.ParentIdx)
	}
	// 4.1 and 4.10 must be distinct.
	if s41.Citation == s410.Citation {
		t.Error("4.1 and 4.10 have the same citation — float-ID hazard!")
	}
	if s41.CitationNorm == s410.CitationNorm {
		t.Error("4.1 and 4.10 have the same citation_norm — float-ID hazard!")
	}

	// --- Safeguard 4.10: IG3 only ---
	if s410.Body == nil {
		t.Fatal("s4.10 body is nil")
	}
	if !strings.Contains(*s410.Body, "Implementation Groups: IG3") {
		t.Errorf("s4.10 IG line wrong: %q", *s410.Body)
	}
	if !strings.Contains(*s410.Body, "Asset Class: Network") {
		t.Errorf("s4.10 missing Asset Class: %q", *s410.Body)
	}
	if !strings.Contains(*s410.Body, "Security Function: Detect") {
		t.Errorf("s4.10 missing Security Function: %q", *s410.Body)
	}

	// --- Safeguard 4.2: missing Asset Class and Security Function and IGs ---
	s42 := tree.Controls[6]
	if s42.Citation != "4.2" {
		t.Errorf("s4.2 citation=%s", s42.Citation)
	}
	if s42.Body == nil {
		t.Fatal("s4.2 body is nil")
	}
	// No Asset Class, Security Function, or IG lines — body is description only.
	if strings.Contains(*s42.Body, "Asset Class:") {
		t.Errorf("s4.2 should not have Asset Class line: %q", *s42.Body)
	}
	if strings.Contains(*s42.Body, "Security Function:") {
		t.Errorf("s4.2 should not have Security Function line: %q", *s42.Body)
	}
	if strings.Contains(*s42.Body, "Implementation Groups:") {
		t.Errorf("s4.2 should not have IG line: %q", *s42.Body)
	}

	// title_original should match title for all (CC BY-NC-ND — verbatim titles allowed).
	for _, c := range tree.Controls {
		if c.TitleOriginal == nil {
			t.Errorf("title_original nil for %s", c.Citation)
		} else if *c.TitleOriginal != c.Title {
			t.Errorf("%s title_original=%q != title=%q", c.Citation, *c.TitleOriginal, c.Title)
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

	// Ordinals monotonically increasing.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not increasing at %d: %d <= %d",
				i, tree.Controls[i].Ordinal, tree.Controls[i-1].Ordinal)
		}
	}
}

func TestBuildCISTree_MissingSheet(t *testing.T) {
	wb := `{"sheets":[{"name":"Other","rows":[]}]}`
	_, err := BuildCISTree(json.RawMessage(wb), "ciscontrols", "v8.1")
	if err == nil {
		t.Fatal("expected error for missing Controls sheet")
	}
	if !strings.Contains(err.Error(), "Controls") {
		t.Errorf("error=%v, want mention of Controls sheet", err)
	}
}

func TestBuildCISTree_Golden(t *testing.T) {
	const workbookPath = "../../data/cis/cis-controls-version-8.1.2-march-2025.xlsx"
	if _, err := os.Stat(workbookPath); os.IsNotExist(err) {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	raw, err := extract.CaptureXLSXFile(workbookPath)
	if err != nil {
		t.Fatalf("CaptureXLSXFile: %v", err)
	}

	tree, err := BuildCISTree(raw, "ciscontrols", "v8.1")
	if err != nil {
		t.Fatalf("BuildCISTree: %v", err)
	}

	// Count by kind.
	var controls, safeguards int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "control":
			controls++
		case "safeguard":
			safeguards++
		default:
			t.Errorf("unexpected kind: %q for %s", c.Kind, c.Citation)
		}
	}

	// Golden count assertions.
	if controls != 18 {
		t.Errorf("controls=%d, want 18", controls)
	}
	if safeguards != 153 {
		t.Errorf("safeguards=%d, want 153", safeguards)
	}
	totalRows := controls + safeguards
	if totalRows != 171 {
		t.Errorf("total rows=%d, want 171", totalRows)
	}

	// No withdrawn rows.
	for _, c := range tree.Controls {
		if c.Status != "active" {
			t.Errorf("unexpected status %q for %s", c.Status, c.Citation)
		}
	}

	// No mapping edges.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// Every safeguard's parent is a control.
	for i, c := range tree.Controls {
		if c.Kind == "safeguard" {
			if c.ParentIdx < 0 || c.ParentIdx >= len(tree.Controls) {
				t.Errorf("safeguard %s parentIdx=%d out of range", c.Citation, c.ParentIdx)
				continue
			}
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "control" {
				t.Errorf("safeguard %s (idx %d) parent kind=%s, want control",
					c.Citation, i, parent.Kind)
			}
		}
	}

	// Float-string discipline: 4.10 exists and is distinct from 4.1.
	var found41, found410 bool
	var idx41, idx410 int
	for i, c := range tree.Controls {
		if c.Citation == "4.1" {
			found41 = true
			idx41 = i
		}
		if c.Citation == "4.10" {
			found410 = true
			idx410 = i
		}
	}
	if !found41 {
		t.Error("safeguard 4.1 not found")
	}
	if !found410 {
		t.Error("safeguard 4.10 not found")
	}
	if found41 && found410 && idx41 == idx410 {
		t.Error("4.1 and 4.10 map to the same control — float-ID hazard!")
	}

	// Spot check: safeguard 1.1 title.
	var s11Title string
	for _, c := range tree.Controls {
		if c.Citation == "1.1" {
			s11Title = c.Title
			break
		}
	}
	if s11Title != "Establish and Maintain Detailed Enterprise Asset Inventory" {
		t.Errorf("safeguard 1.1 title=%q, want 'Establish and Maintain Detailed Enterprise Asset Inventory'",
			s11Title)
	}

	// IG rendering spot-check on safeguard 1.1 (IG1, IG2, IG3).
	var s11Body string
	for _, c := range tree.Controls {
		if c.Citation == "1.1" && c.Body != nil {
			s11Body = *c.Body
			break
		}
	}
	if !strings.Contains(s11Body, "Implementation Groups: IG1, IG2, IG3") {
		t.Errorf("safeguard 1.1 IG line not found in body: %q", s11Body)
	}

	// IG rendering spot-check on safeguard 1.5 (IG3 only).
	var s15Body string
	for _, c := range tree.Controls {
		if c.Citation == "1.5" && c.Body != nil {
			s15Body = *c.Body
			break
		}
	}
	if !strings.Contains(s15Body, "Implementation Groups: IG3") {
		t.Errorf("safeguard 1.5 IG line not found in body: %q", s15Body)
	}
	if strings.Contains(s15Body, "IG1") || strings.Contains(s15Body, "IG2,") {
		t.Errorf("safeguard 1.5 should only have IG3: %q", s15Body)
	}

	// title_original set for all controls.
	for _, c := range tree.Controls {
		if c.TitleOriginal == nil {
			t.Errorf("title_original nil for %s", c.Citation)
		}
	}

	// Every control has a body (description).
	for _, c := range tree.Controls {
		if c.Kind == "control" && c.Body == nil {
			t.Errorf("control %s has nil body", c.Citation)
		}
	}

	// Every safeguard has a body.
	for _, c := range tree.Controls {
		if c.Kind == "safeguard" && c.Body == nil {
			t.Errorf("safeguard %s has nil body", c.Citation)
		}
	}
}
