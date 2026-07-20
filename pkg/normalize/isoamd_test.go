package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

// syntheticISOAmd mirrors the shape of ISO/IEC 27001:2022/Amd 1:2024: front
// matter pages, then one instruction page with two edits, then a back page.
const syntheticISOAmd = `{
  "pages": [
    {"n": 1, "text": "Information security — Requirements\nAMENDMENT 1: Example changes\nInternational Standard\n"},
    {"n": 2, "text": "Foreword\nISO and IEC form the specialized system for worldwide standardization.\n"},
    {"n": 3, "text": "AMENDMENT 1: Example changes\n4.1\nAdd the following sentence at the end of the subclause:\nThe organization shall determine whether the example topic is a relevant issue.\n4.2\nAdd the following note at the end of the subclause:\nNOTE 2 Relevant interested parties can have requirements related to the example topic.\n1\n© Example 2024 – All rights reserved\n"},
    {"n": 4, "text": "ICS 03.100.70\nPrice based on 1 page\n"}
  ]
}`

func TestBuildISOAmendmentTree_Synthetic(t *testing.T) {
	tree, err := BuildISOAmendmentTree(json.RawMessage(syntheticISOAmd), "iso27001", "2022")
	if err != nil {
		t.Fatalf("BuildISOAmendmentTree: %v", err)
	}

	if len(tree.Controls) != 2 {
		t.Fatalf("controls=%d, want 2; got %+v", len(tree.Controls), tree.Controls)
	}

	c0 := tree.Controls[0]
	if c0.Citation != "4.1" || c0.CitationNorm != "4.1" {
		t.Errorf("c0 citation=%s/%s, want 4.1", c0.Citation, c0.CitationNorm)
	}
	if c0.AmendsCitationNorm == nil || *c0.AmendsCitationNorm != "4.1" {
		t.Errorf("c0 amends=%v, want 4.1", c0.AmendsCitationNorm)
	}
	if c0.AmendAction == nil || *c0.AmendAction != "add" {
		t.Errorf("c0 action=%v, want add", c0.AmendAction)
	}
	if c0.Kind != "clause" || c0.Status != "active" || c0.ParentIdx != -1 {
		t.Errorf("c0 kind/status/parent = %s/%s/%d", c0.Kind, c0.Status, c0.ParentIdx)
	}
	// Title is a generated neutral label — never amendment text.
	if c0.Title != "Amendment change to clause 4.1" {
		t.Errorf("c0 title=%q", c0.Title)
	}
	if c0.Body == nil || !strings.Contains(*c0.Body, "example topic is a relevant issue") {
		t.Errorf("c0 body missing amended text: %v", c0.Body)
	}
	if strings.Contains(*c0.Body, "All rights reserved") {
		t.Errorf("c0 body contains footer noise: %q", *c0.Body)
	}

	c1 := tree.Controls[1]
	if c1.Citation != "4.2" {
		t.Errorf("c1 citation=%s, want 4.2", c1.Citation)
	}
	if c1.Body == nil || !strings.Contains(*c1.Body, "NOTE 2") {
		t.Errorf("c1 body missing note text: %v", c1.Body)
	}
	// The 4.2 body must not have swallowed the page-number or copyright lines.
	if strings.Contains(*c1.Body, "Price based") || strings.Contains(*c1.Body, "rights reserved") {
		t.Errorf("c1 body contains trailing noise: %q", *c1.Body)
	}
}

func TestBuildISOAmendmentTree_NoInstructions(t *testing.T) {
	empty := `{"pages":[{"n":1,"text":"Foreword\nNothing amendable here.\n"}]}`
	if _, err := BuildISOAmendmentTree(json.RawMessage(empty), "iso27001", "2022"); err == nil {
		t.Fatal("expected error for capture without instructions")
	}
}
