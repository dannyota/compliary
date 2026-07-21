package normalize

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/extract"
)

// syntheticTSC is a minimal pdf-pages-json fixture covering:
// - CC-series criterion (CC1.1) with PoF bullets
// - A mid-line criterion (PI1.1) preceded by section header text on same line
// - A P-series criterion (P1.1) with [C] and [P] marker bullets
// - Privacy category header (P1.0) that is NOT a criterion
//
// ALL WORDING IS INVENTED — no AICPA normative text.
const syntheticTSC = `{
  "pages": [
    {
      "n": 1,
      "text": "TSP Section 100\nInvented Title Page\n"
    },
    {
      "n": 14,
      "text": "TSP Ref. #\nTRUST SERVICES CRITERIA AND POINTS OF FOCUS\n CONTROL ENVIRONMENT\nCC1.1 Invented principle about integrity.\n\n The following points of focus highlight important characteristics:\n\n• Invented Lead-In Alpha — Invented description of tone at top.\n\n• Invented Lead-In Beta — Invented description of conduct standards.\n\n• Invented Lead-In Gamma — Invented supplemental point.\n"
    },
    {
      "n": 15,
      "text": "CC1.2 Invented principle about board oversight.\n\n• Invented Lead-In Delta — Invented oversight description.\n\n• Invented Lead-In Epsilon — Invented independence point.\n"
    },
    {
      "n": 30,
      "text": "CC9.1 Invented principle about risk mitigation activities.\n\n• Invented Lead-In Zeta — Invented risk activity description.\n"
    },
    {
      "n": 40,
      "text": "ADDITIONAL CRITERIA FOR AVAILABILITY\nA1.1 Invented availability criterion about capacity management.\n\n• Invented Lead-In Eta — Invented capacity point.\n\n• Invented Lead-In Theta — Invented monitoring point.\n"
    },
    {
      "n": 45,
      "text": "C1.1 Invented confidentiality criterion about identification.\n\n• Invented Lead-In Iota [C] — Invented confidentiality point.\n"
    },
    {
      "n": 50,
      "text": "ADDITIONAL CRITERIA FOR PROCESSING INTEGRITY Invented section header text PI1.1 Invented processing integrity criterion about data quality.\n\n• Invented Lead-In Kappa — Invented data quality point.\n\n• Invented Lead-In Lambda — Invented specification point.\n"
    },
    {
      "n": 55,
      "text": "PI1.2 Invented processing integrity criterion about inputs.\n\n• Invented Lead-In Mu — Invented input control point.\n"
    },
    {
      "n": 60,
      "text": "P1.0 Invented Privacy Category Header for Notice\nP1.1 Invented privacy criterion about notice to data subjects.\n\n• Invented Lead-In Nu [C] — Invented notice provision point.\n\n• Invented Lead-In Xi [P] — Invented data controller notice point.\n\n• Invented Lead-In Omicron — Invented general privacy point.\n"
    }
  ]
}`

func TestBuildTSCTree_Synthetic(t *testing.T) {
	tree, err := BuildTSCTree(json.RawMessage(syntheticTSC), "soc2tsc", "2017")
	if err != nil {
		t.Fatalf("BuildTSCTree: %v", err)
	}

	// Title.
	if tree.Title != "TSC 2017" {
		t.Errorf("title=%q, want %q", tree.Title, "TSC 2017")
	}

	// Expected structure:
	// CC1.1 (criterion) + 3 PoFs
	// CC1.2 (criterion) + 2 PoFs
	// CC9.1 (criterion) + 1 PoF
	// A1.1 (criterion) + 2 PoFs
	// C1.1 (criterion) + 1 PoF
	// PI1.1 (criterion, mid-line) + 2 PoFs
	// PI1.2 (criterion) + 1 PoF
	// P1.1 (criterion, NOT P1.0) + 3 PoFs
	// Total: 8 criteria + 15 PoFs = 23 rows
	wantCriteria := 8
	wantPoFs := 15
	wantTotal := wantCriteria + wantPoFs

	if len(tree.Controls) != wantTotal {
		t.Fatalf("controls=%d, want %d; got: %v", len(tree.Controls), wantTotal, controlIDs(tree.Controls))
	}

	// Count by kind.
	criteriaCount := 0
	pofCount := 0
	for _, c := range tree.Controls {
		switch c.Kind {
		case "criterion":
			criteriaCount++
		case "point-of-focus":
			pofCount++
		default:
			t.Errorf("unexpected kind %q for %s", c.Kind, c.Citation)
		}
	}
	if criteriaCount != wantCriteria {
		t.Errorf("criteria=%d, want %d", criteriaCount, wantCriteria)
	}
	if pofCount != wantPoFs {
		t.Errorf("pofs=%d, want %d", pofCount, wantPoFs)
	}

	// Criteria are roots (ParentIdx == -1).
	for _, c := range tree.Controls {
		if c.Kind == "criterion" && c.ParentIdx != -1 {
			t.Errorf("criterion %s has parentIdx=%d, want -1 (roots)", c.Citation, c.ParentIdx)
		}
	}

	// PoFs have a criterion parent.
	for _, c := range tree.Controls {
		if c.Kind == "point-of-focus" {
			if c.ParentIdx < 0 {
				t.Errorf("PoF %s has no parent", c.Citation)
				continue
			}
			parent := tree.Controls[c.ParentIdx]
			if parent.Kind != "criterion" {
				t.Errorf("PoF %s parent kind=%s, want criterion", c.Citation, parent.Kind)
			}
		}
	}

	// --- CC1.1 criterion ---
	cc11 := findByCitation(tree.Controls, "CC1.1")
	if cc11 == nil {
		t.Fatal("CC1.1 not found")
	}
	if cc11.Title != "Criterion CC1.1" {
		t.Errorf("CC1.1 title=%q, want 'Criterion CC1.1'", cc11.Title)
	}
	if cc11.TitleOriginal != nil {
		t.Errorf("CC1.1 title_original=%v, want nil", cc11.TitleOriginal)
	}
	if cc11.Status != "active" {
		t.Errorf("CC1.1 status=%s, want active", cc11.Status)
	}
	// CC1.1 body should contain criterion text.
	if cc11.Body == nil {
		t.Error("CC1.1 body is nil")
	}

	// CC1.1 should have 3 PoF children.
	cc11Idx := indexByCitation(tree.Controls, "CC1.1")
	cc11PoFs := childrenOf(tree.Controls, cc11Idx)
	if len(cc11PoFs) != 3 {
		t.Errorf("CC1.1 PoFs=%d, want 3", len(cc11PoFs))
	}

	// PoF citations follow the pattern CC1.1-pof-01, CC1.1-pof-02, CC1.1-pof-03.
	for i, pof := range cc11PoFs {
		wantCite := fmt.Sprintf("CC1.1-pof-%02d", i+1)
		if pof.Citation != wantCite {
			t.Errorf("CC1.1 PoF[%d] citation=%s, want %s", i, pof.Citation, wantCite)
		}
	}

	// PoF titles are neutral: "CC1.1 point of focus 1", etc.
	for i, pof := range cc11PoFs {
		wantTitle := fmt.Sprintf("CC1.1 point of focus %d", i+1)
		if pof.Title != wantTitle {
			t.Errorf("CC1.1 PoF[%d] title=%q, want %q", i, pof.Title, wantTitle)
		}
	}

	// PoF title_original should carry the lead-in phrase (auth-gated).
	if cc11PoFs[0].TitleOriginal == nil {
		t.Error("CC1.1 PoF 1 title_original is nil (should be lead-in phrase)")
	}

	// --- PI1.1 mid-line recovery ---
	pi11 := findByCitation(tree.Controls, "PI1.1")
	if pi11 == nil {
		t.Fatal("PI1.1 not found — mid-line recovery failed")
	}
	if pi11.Kind != "criterion" {
		t.Errorf("PI1.1 kind=%s, want criterion", pi11.Kind)
	}
	if pi11.ParentIdx != -1 {
		t.Errorf("PI1.1 parentIdx=%d, want -1 (root)", pi11.ParentIdx)
	}
	pi11Idx := indexByCitation(tree.Controls, "PI1.1")
	pi11PoFs := childrenOf(tree.Controls, pi11Idx)
	if len(pi11PoFs) != 2 {
		t.Errorf("PI1.1 PoFs=%d, want 2", len(pi11PoFs))
	}

	// --- P1.0 should NOT appear (category header, not a criterion) ---
	p10 := findByCitation(tree.Controls, "P1.0")
	if p10 != nil {
		t.Error("P1.0 should not appear in tree (category header, not a criterion)")
	}

	// --- P1.1 should exist ---
	p11 := findByCitation(tree.Controls, "P1.1")
	if p11 == nil {
		t.Fatal("P1.1 not found")
	}
	if p11.Kind != "criterion" {
		t.Errorf("P1.1 kind=%s, want criterion", p11.Kind)
	}
	p11Idx := indexByCitation(tree.Controls, "P1.1")
	p11PoFs := childrenOf(tree.Controls, p11Idx)
	if len(p11PoFs) != 3 {
		t.Errorf("P1.1 PoFs=%d, want 3", len(p11PoFs))
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

	// No mappings (TSC parser emits none).
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}
}

func TestBuildTSCTree_EmptyCapture(t *testing.T) {
	empty := `{"pages":[]}`
	_, err := BuildTSCTree(json.RawMessage(empty), "soc2tsc", "2017")
	if err == nil {
		t.Fatal("expected error for empty capture")
	}
	if !strings.Contains(err.Error(), "no criteria") {
		t.Errorf("error=%v, want mention of no criteria", err)
	}
}

func TestBuildTSCTree_InvalidJSON(t *testing.T) {
	_, err := BuildTSCTree(json.RawMessage(`{bad json}`), "soc2tsc", "2017")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuildTSCTree_Golden(t *testing.T) {
	const pdfPath = "../../data/aicpa/aicpa-tsc-2017-points-of-focus-2022.pdf"
	raw, err := extract.CapturePDFFile(pdfPath)
	if err != nil {
		t.Skipf("data file absent (expected for non-maintainer): %v", err)
	}

	tree, err := BuildTSCTree(json.RawMessage(raw), "soc2tsc", "2017")
	if err != nil {
		t.Fatalf("BuildTSCTree: %v", err)
	}

	// --- Golden count pins ---

	// Count criteria and PoFs.
	var criteria, pofs int
	seriesCounts := map[string]int{}

	for _, c := range tree.Controls {
		switch c.Kind {
		case "criterion":
			criteria++
			// Series classification.
			switch {
			case strings.HasPrefix(c.Citation, "CC"):
				seriesCounts["CC"]++
			case strings.HasPrefix(c.Citation, "PI"):
				seriesCounts["PI"]++
			case strings.HasPrefix(c.Citation, "A"):
				seriesCounts["A"]++
			case strings.HasPrefix(c.Citation, "C"):
				seriesCounts["C"]++
			case strings.HasPrefix(c.Citation, "P"):
				seriesCounts["P"]++
			}
		case "point-of-focus":
			pofs++
		default:
			t.Errorf("unexpected kind %q for %s", c.Kind, c.Citation)
		}
	}

	// Official TSC 2017 criteria counts.
	if criteria != 61 {
		t.Errorf("total criteria=%d, want 61", criteria)
	}
	if seriesCounts["CC"] != 33 {
		t.Errorf("CC criteria=%d, want 33", seriesCounts["CC"])
	}
	if seriesCounts["A"] != 3 {
		t.Errorf("A criteria=%d, want 3", seriesCounts["A"])
	}
	if seriesCounts["C"] != 2 {
		t.Errorf("C criteria=%d, want 2", seriesCounts["C"])
	}
	if seriesCounts["PI"] != 5 {
		t.Errorf("PI criteria=%d, want 5", seriesCounts["PI"])
	}
	if seriesCounts["P"] != 18 {
		t.Errorf("P criteria=%d, want 18", seriesCounts["P"])
	}

	// PoF total: 332 (from survey of the 2022 revised PoF edition).
	if pofs != 332 {
		t.Errorf("total PoFs=%d, want 332", pofs)
	}

	// Total rows: 61 criteria + 332 PoFs = 393.
	totalRows := len(tree.Controls)
	if totalRows != 393 {
		t.Errorf("total rows=%d, want 393", totalRows)
	}

	// All criteria are roots (parentIdx == -1).
	for _, c := range tree.Controls {
		if c.Kind == "criterion" && c.ParentIdx != -1 {
			t.Errorf("criterion %s has parentIdx=%d, want -1", c.Citation, c.ParentIdx)
		}
	}

	// All PoFs have a criterion parent.
	for _, c := range tree.Controls {
		if c.Kind == "point-of-focus" && c.ParentIdx < 0 {
			t.Errorf("PoF %s has no parent", c.Citation)
		}
	}

	// PoF citations are <criterion>-pof-NN with zero-padded ordinals.
	for _, c := range tree.Controls {
		if c.Kind == "point-of-focus" {
			if !strings.Contains(c.Citation, "-pof-") {
				t.Errorf("PoF citation %q missing '-pof-' pattern", c.Citation)
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

	// Ordinal monotonicity.
	for i := 1; i < len(tree.Controls); i++ {
		if tree.Controls[i].Ordinal <= tree.Controls[i-1].Ordinal {
			t.Errorf("ordinal not monotonic: %s ord=%d, %s ord=%d",
				tree.Controls[i-1].Citation, tree.Controls[i-1].Ordinal,
				tree.Controls[i].Citation, tree.Controls[i].Ordinal)
		}
	}

	// Criteria have neutral titles: "Criterion <citation>".
	for _, c := range tree.Controls {
		if c.Kind == "criterion" {
			want := "Criterion " + c.Citation
			if c.Title != want {
				t.Errorf("%s title=%q, want %q", c.Citation, c.Title, want)
			}
		}
	}

	// Criteria have nil title_original (AICPA licensed text).
	for _, c := range tree.Controls {
		if c.Kind == "criterion" && c.TitleOriginal != nil {
			t.Errorf("criterion %s title_original=%v, want nil", c.Citation, c.TitleOriginal)
		}
	}

	// Criteria have non-nil body.
	for _, c := range tree.Controls {
		if c.Kind == "criterion" && c.Body == nil {
			t.Errorf("criterion %s body is nil", c.Citation)
		}
	}

	// Spot-check: CC1.1 should have 5 PoFs.
	cc11Idx := indexByCitation(tree.Controls, "CC1.1")
	if cc11Idx < 0 {
		t.Fatal("CC1.1 not found")
	}
	cc11PoFs := childrenOf(tree.Controls, cc11Idx)
	if len(cc11PoFs) != 5 {
		t.Errorf("CC1.1 PoFs=%d, want 5", len(cc11PoFs))
	}

	// Spot-check: CC8.1 should have 18 PoFs.
	cc81Idx := indexByCitation(tree.Controls, "CC8.1")
	if cc81Idx < 0 {
		t.Fatal("CC8.1 not found")
	}
	cc81PoFs := childrenOf(tree.Controls, cc81Idx)
	if len(cc81PoFs) != 18 {
		t.Errorf("CC8.1 PoFs=%d, want 18", len(cc81PoFs))
	}

	// Spot-check: PI1.1 exists (mid-line recovery) and has 3 PoFs.
	pi11 := findByCitation(tree.Controls, "PI1.1")
	if pi11 == nil {
		t.Fatal("PI1.1 not found — mid-line recovery failed")
	}
	if pi11.Kind != "criterion" {
		t.Errorf("PI1.1 kind=%s, want criterion", pi11.Kind)
	}
	pi11Idx := indexByCitation(tree.Controls, "PI1.1")
	pi11PoFs := childrenOf(tree.Controls, pi11Idx)
	if len(pi11PoFs) != 3 {
		t.Errorf("PI1.1 PoFs=%d, want 3", len(pi11PoFs))
	}

	// Spot-check: Px.0 category headers are NOT criteria.
	for _, citeID := range []string{"P1.0", "P2.0", "P3.0", "P4.0", "P5.0", "P6.0", "P7.0", "P8.0"} {
		if findByCitation(tree.Controls, citeID) != nil {
			t.Errorf("%s should not appear in tree (category header, not a criterion)", citeID)
		}
	}

	// No mappings.
	if len(tree.Mappings) != 0 {
		t.Errorf("mappings=%d, want 0", len(tree.Mappings))
	}

	// PoF title_original populated (lead-in phrases): 328/332 have lead-ins.
	var pofWithTitle int
	for _, c := range tree.Controls {
		if c.Kind == "point-of-focus" && c.TitleOriginal != nil {
			pofWithTitle++
		}
	}
	if pofWithTitle != 328 {
		t.Errorf("PoFs with title_original=%d, want 328", pofWithTitle)
	}
}

// TestBuildTSCTree_PreamblePrefixInBody verifies that a PoF continuation line
// starting with a category-name word (e.g. "Risk Assessment procedures...") is
// NOT dropped by the preamble filter. Only exact standalone section headers
// like "RISK ASSESSMENT" (the entire line) are filtered.
func TestBuildTSCTree_PreamblePrefixInBody(t *testing.T) {
	fixture := `{
  "pages": [
    {
      "n": 14,
      "text": "CC1.1 Invented principle about integrity.\n\n• Invented Lead-In Alpha — Risk Assessment procedures should be reviewed\nperiodically to ensure fictional completeness.\n"
    }
  ]
}`
	tree, err := BuildTSCTree(json.RawMessage(fixture), "soc2tsc", "2017")
	if err != nil {
		t.Fatalf("BuildTSCTree: %v", err)
	}

	// Should have CC1.1 criterion + 1 PoF = 2 rows.
	if len(tree.Controls) != 2 {
		t.Fatalf("controls=%d, want 2; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}

	pof := tree.Controls[1]
	if pof.Kind != "point-of-focus" {
		t.Fatalf("control[1] kind=%s, want point-of-focus", pof.Kind)
	}
	if pof.Body == nil {
		t.Fatal("PoF body is nil")
	}

	// The continuation line starting with "Risk Assessment" must be preserved.
	if !strings.Contains(*pof.Body, "Risk Assessment procedures") {
		t.Error("PoF body lost continuation line starting with 'Risk Assessment' — preamble filter too broad")
	}
}

// TestBuildTSCTree_PreambleExactMatch verifies that exact standalone section
// headers (e.g. "RISK ASSESSMENT" as the entire line) are still filtered.
func TestBuildTSCTree_PreambleExactMatch(t *testing.T) {
	fixture := `{
  "pages": [
    {
      "n": 14,
      "text": "CC1.1 Invented principle about integrity.\nRISK ASSESSMENT\n\n• Invented Lead-In Alpha — Invented description.\n"
    }
  ]
}`
	tree, err := BuildTSCTree(json.RawMessage(fixture), "soc2tsc", "2017")
	if err != nil {
		t.Fatalf("BuildTSCTree: %v", err)
	}

	// CC1.1 body should not contain the standalone header.
	cc11 := findByCitation(tree.Controls, "CC1.1")
	if cc11 == nil {
		t.Fatal("CC1.1 not found")
	}
	if cc11.Body != nil && strings.Contains(*cc11.Body, "RISK ASSESSMENT") {
		t.Error("standalone 'RISK ASSESSMENT' header leaked into criterion body")
	}
}

// --- test helpers ---

func findByCitation(controls []ControlRow, citation string) *ControlRow {
	for i := range controls {
		if controls[i].Citation == citation {
			return &controls[i]
		}
	}
	return nil
}

func indexByCitation(controls []ControlRow, citation string) int {
	for i, c := range controls {
		if c.Citation == citation {
			return i
		}
	}
	return -1
}

func childrenOf(controls []ControlRow, parentIdx int) []ControlRow {
	var children []ControlRow
	for _, c := range controls {
		if c.ParentIdx == parentIdx {
			children = append(children, c)
		}
	}
	return children
}
