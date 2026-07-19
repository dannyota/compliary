package normalize

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// syntheticOSCAL is a minimal OSCAL catalog fixture with:
// - params (Assignment + Selection rendering)
// - withdrawn control with incorporated-into link
// - enhancement nesting
// - guidance part
const syntheticOSCAL = `{
  "catalog": {
    "uuid": "test-uuid",
    "metadata": {
      "title": "Synthetic Test Catalog",
      "version": "1.0.0",
      "oscal-version": "1.2.2"
    },
    "groups": [
      {
        "id": "sy",
        "title": "Synthetic Family",
        "props": [{"name": "label", "value": "SY"}],
        "controls": [
          {
            "id": "sy-1",
            "title": "First Control",
            "props": [{"name": "label", "value": "SY-01"}],
            "params": [
              {"id": "sy-1_prm_1", "label": "organization-defined frequency"},
              {"id": "sy-1_prm_2", "select": {"how-many": "one-or-more", "choice": ["option A", "option B", "option C"]}},
              {"id": "sy-1_prm_3", "select": {"choice": ["daily", "weekly"]}}
            ],
            "parts": [
              {
                "id": "sy-1_stmt",
                "name": "statement",
                "prose": "Implement controls {{ insert: param, sy-1_prm_1 }} using {{ insert: param, sy-1_prm_2 }} on a {{ insert: param, sy-1_prm_3 }} basis."
              },
              {
                "id": "sy-1_gdn",
                "name": "guidance",
                "prose": "This is guidance text for the first control."
              }
            ],
            "controls": [
              {
                "id": "sy-1.1",
                "title": "Enhancement Alpha",
                "props": [{"name": "label", "value": "SY-01(01)"}],
                "parts": [
                  {
                    "id": "sy-1.1_stmt",
                    "name": "statement",
                    "prose": "Enhancement prose here."
                  }
                ]
              }
            ]
          },
          {
            "id": "sy-2",
            "title": "Second Control",
            "props": [
              {"name": "label", "value": "SY-02"},
              {"name": "status", "value": "withdrawn"}
            ],
            "links": [
              {"href": "#sy-1", "rel": "incorporated-into"}
            ],
            "parts": [
              {
                "id": "sy-2_stmt",
                "name": "statement",
                "prose": "Withdrawn control text."
              }
            ]
          },
          {
            "id": "sy-3",
            "title": "Third Control",
            "props": [{"name": "label", "value": "SY-03"}],
            "controls": [
              {
                "id": "sy-3.1",
                "title": "Enhancement Beta",
                "props": [
                  {"name": "label", "value": "SY-03(01)"},
                  {"name": "status", "value": "withdrawn"}
                ],
                "links": [
                  {"href": "#sy-1.1", "rel": "moved-to"}
                ]
              }
            ]
          }
        ]
      }
    ]
  }
}`

func TestBuildOSCALTree_Synthetic(t *testing.T) {
	tree, err := BuildOSCALTree(json.RawMessage(syntheticOSCAL), "synthfw", "v1")
	if err != nil {
		t.Fatalf("BuildOSCALTree: %v", err)
	}

	// Title.
	if tree.Title != "Synthetic Test Catalog" {
		t.Errorf("title=%q, want %q", tree.Title, "Synthetic Test Catalog")
	}

	// Expected rows: 1 family + 3 controls + 2 enhancements = 6.
	if len(tree.Controls) != 6 {
		t.Fatalf("controls=%d, want 6", len(tree.Controls))
	}

	// Verify family.
	fam := tree.Controls[0]
	if fam.Kind != "family" || fam.Citation != "SY" || fam.CitationNorm != "SY" {
		t.Errorf("family: kind=%s citation=%s norm=%s", fam.Kind, fam.Citation, fam.CitationNorm)
	}
	if fam.ParentIdx != -1 {
		t.Errorf("family parentIdx=%d, want -1", fam.ParentIdx)
	}

	// Verify first control.
	c1 := tree.Controls[1]
	if c1.Kind != "control" || c1.Citation != "SY-01" || c1.Status != "active" {
		t.Errorf("c1: kind=%s citation=%s status=%s", c1.Kind, c1.Citation, c1.Status)
	}
	if c1.ParentIdx != 0 {
		t.Errorf("c1 parentIdx=%d, want 0 (family)", c1.ParentIdx)
	}

	// Check param rendering in body.
	if c1.Body == nil {
		t.Fatal("c1.Body is nil")
	}
	body := *c1.Body
	if !strings.Contains(body, "[Assignment: organization-defined frequency]") {
		t.Errorf("c1 body missing Assignment rendering: %s", body)
	}
	if !strings.Contains(body, "[Selection (one or more): option A; option B; option C]") {
		t.Errorf("c1 body missing Selection (one or more) rendering: %s", body)
	}
	if !strings.Contains(body, "[Selection: daily; weekly]") {
		t.Errorf("c1 body missing plain Selection rendering: %s", body)
	}
	if !strings.Contains(body, "This is guidance text") {
		t.Errorf("c1 body missing guidance: %s", body)
	}

	// Verify enhancement.
	enh := tree.Controls[2]
	if enh.Kind != "enhancement" || enh.Citation != "SY-01(01)" {
		t.Errorf("enh: kind=%s citation=%s", enh.Kind, enh.Citation)
	}
	if enh.ParentIdx != 1 {
		t.Errorf("enh parentIdx=%d, want 1 (control SY-01)", enh.ParentIdx)
	}

	// Verify withdrawn control.
	c2 := tree.Controls[3]
	if c2.Kind != "control" || c2.Citation != "SY-02" || c2.Status != "withdrawn" {
		t.Errorf("c2: kind=%s citation=%s status=%s", c2.Kind, c2.Citation, c2.Status)
	}

	// Verify withdrawn enhancement.
	enhW := tree.Controls[5]
	if enhW.Kind != "enhancement" || enhW.Citation != "SY-03(01)" || enhW.Status != "withdrawn" {
		t.Errorf("enhW: kind=%s citation=%s status=%s", enhW.Kind, enhW.Citation, enhW.Status)
	}

	// Verify mappings.
	if len(tree.Mappings) != 2 {
		t.Fatalf("mappings=%d, want 2", len(tree.Mappings))
	}

	// First mapping: SY-02 → incorporated-into → SY-01.
	m0 := tree.Mappings[0]
	if m0.FromIdx != 3 {
		t.Errorf("m0.FromIdx=%d, want 3 (SY-02)", m0.FromIdx)
	}
	if m0.Relationship != "incorporated-into" {
		t.Errorf("m0.Relationship=%s, want incorporated-into", m0.Relationship)
	}
	if m0.ToCitationNorm != "SY-01" {
		t.Errorf("m0.ToCitationNorm=%s, want SY-01", m0.ToCitationNorm)
	}
	if m0.ToFrameworkCode != "synthfw" {
		t.Errorf("m0.ToFrameworkCode=%s, want synthfw", m0.ToFrameworkCode)
	}
	if m0.ToVersionLabel == nil || *m0.ToVersionLabel != "v1" {
		t.Errorf("m0.ToVersionLabel=%v, want v1", m0.ToVersionLabel)
	}
	if m0.ProvenanceDetail != "#sy-1" {
		t.Errorf("m0.ProvenanceDetail=%s, want #sy-1", m0.ProvenanceDetail)
	}

	// Second mapping: SY-03(01) → moved-to → SY-01(01).
	m1 := tree.Mappings[1]
	if m1.FromIdx != 5 {
		t.Errorf("m1.FromIdx=%d, want 5 (SY-03(01))", m1.FromIdx)
	}
	if m1.Relationship != "moved-to" {
		t.Errorf("m1.Relationship=%s, want moved-to", m1.Relationship)
	}
	if m1.ToCitationNorm != "SY-01(01)" {
		t.Errorf("m1.ToCitationNorm=%s, want SY-01(01)", m1.ToCitationNorm)
	}
	if m1.ToFrameworkCode != "synthfw" {
		t.Errorf("m1.ToFrameworkCode=%s, want synthfw", m1.ToFrameworkCode)
	}

	// citation_norm uniqueness.
	norms := map[string]bool{}
	for _, c := range tree.Controls {
		if norms[c.CitationNorm] {
			t.Errorf("duplicate citation_norm: %s", c.CitationNorm)
		}
		norms[c.CitationNorm] = true
	}

	// title_original set for all.
	for _, c := range tree.Controls {
		if c.TitleOriginal == nil {
			t.Errorf("title_original nil for %s", c.Citation)
		}
	}
}

func TestBuildOSCALTree_Golden(t *testing.T) {
	const catalogPath = "../../data/nist/nist-sp-800-53r5-oscal-catalog.json"
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildOSCALTree(json.RawMessage(raw), "nist80053", "r5")
	if err != nil {
		t.Fatalf("BuildOSCALTree: %v", err)
	}

	// Count by kind.
	var families, controls, enhancements int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "family":
			families++
		case "control":
			controls++
		case "enhancement":
			enhancements++
		}
	}

	// Golden assertions.
	if families != 20 {
		t.Errorf("families=%d, want 20", families)
	}
	if controls != 324 {
		t.Errorf("controls=%d, want 324", controls)
	}
	if enhancements != 872 {
		t.Errorf("enhancements=%d, want 872", enhancements)
	}
	totalRows := families + controls + enhancements
	if totalRows != 1216 {
		t.Errorf("total rows=%d, want 1216", totalRows)
	}

	// Withdrawn count.
	var withdrawn int
	for _, c := range tree.Controls {
		if c.Status == "withdrawn" {
			withdrawn++
		}
	}
	if withdrawn != 182 {
		t.Errorf("withdrawn=%d, want 182", withdrawn)
	}

	// Max depth: enhancements never nest deeper than 1 level below control.
	for i, c := range tree.Controls {
		if c.Kind == "enhancement" && c.ParentIdx >= 0 {
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "control" {
				t.Errorf("enhancement %s (idx=%d) parent is %s (kind=%s), want control",
					c.Citation, i, parent.Citation, parent.Kind)
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

	// Mapping count: 200 total edges (166 incorporated-into + 34 moved-to).
	if len(tree.Mappings) != 200 {
		t.Errorf("mappings=%d, want 200", len(tree.Mappings))
	}

	// Verify all mappings point to valid citation_norms in the tree.
	normSet := map[string]bool{}
	for _, c := range tree.Controls {
		normSet[c.CitationNorm] = true
	}
	var unresolvable int
	for _, m := range tree.Mappings {
		if !normSet[m.ToCitationNorm] {
			unresolvable++
		}
	}
	// Some edges point to withdrawn controls — that's valid (they exist in the tree).
	// Report if any target doesn't exist at all.
	if unresolvable > 0 {
		t.Errorf("unresolvable mapping targets: %d", unresolvable)
	}

	// Unresolved links: the real catalog should have zero.
	if len(tree.UnresolvedLinks) != 0 {
		t.Errorf("unresolved links=%d, want 0", len(tree.UnresolvedLinks))
		for _, ul := range tree.UnresolvedLinks {
			t.Logf("  unresolved: citation=%s href=%s", ul.Citation, ul.Href)
		}
	}
}
