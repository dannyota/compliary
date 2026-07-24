package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

// syntheticTCVN is a minimal pdf-pages-json fixture that mirrors the STRUCTURAL
// shape of TCVN 11930:2017 without reproducing any licensed normative text.
//
// ALL VIETNAMESE WORDING IS INVENTED placeholder content. It must never be
// copied from the real standard — a prior round failed review for real headings
// in fixtures.
//
// Structure exercised:
//   - Front matter that MUST be excluded: sections 1 (scope) and 4 (intro),
//     a term definition 3.1, a table-of-contents line (dot leaders).
//   - A page-header artifact ("TCVN 11930:2017") and a bare page number that
//     must be stripped from bodies.
//   - Main body levels 5 and 6 with management (X.1) and technical (X.2)
//     groups, requirement subgroups (X.Y.Z) and leaf requirement clauses
//     (X.Y.Z.W) — the citable controls. One leaf body carries a) / b) bullets.
//   - A leading-zero line ("06 thang ...") inside a body that must NOT be
//     mistaken for a clause number.
//   - Annex A: level headings A.N and leaf clauses A.N.M, including a
//     multi-digit last segment (A.2.10).
const syntheticTCVN = `{
  "pages": [
    {
      "n": 1,
      "text": "T I E U  C H U A N  Q U O C  G I A\nTCVN 11930:2017\nBia va trang bia dat hang test.\n"
    },
    {
      "n": 3,
      "text": "Muc luc\n5.1 Muc quan ly dat hang .......... 15\n5.1.1 Nhom dat hang .......... 15\n"
    },
    {
      "n": 9,
      "text": "1 Pham vi ap dung dat hang\nNoi dung pham vi dat hang khong phai la dieu khoan.\n3 Thuat ngu va dinh nghia\n3.1\nThuat ngu dat hang (invented term)\nDinh nghia thuat ngu dat hang khong phai la yeu cau.\n4 Yeu cau co ban theo cap do dat hang\nDoan gioi thieu dat hang khong phai la mot yeu cau.\n"
    },
    {
      "n": 15,
      "text": "TCVN 11930:2017\n15\n5 Yeu cau co ban cho he thong cap do mot dat hang\n5.1 Yeu cau quan ly dat hang\n5.1.1 Thiet lap nhom chinh sach dat hang\n5.1.1.1 Chinh sach dat hang mot\nNoi dung yeu cau chinh sach dat hang so mot invented.\n5.1.1.2 Xay dung va cong bo dat hang\na) Muc dat hang thu nhat cua yeu cau nay;\nb) Muc dat hang thu hai luu tru toi thieu 06 thang dat hang.\n5.1.2 Thiet lap nhom to chuc dat hang\n5.1.2.1 Don vi chuyen trach dat hang\nNoi dung yeu cau don vi chuyen trach dat hang invented.\n"
    },
    {
      "n": 17,
      "text": "TCVN 11930:2017\n17\n5.2 Yeu cau ky thuat dat hang\n5.2.1 Bao dam an toan mang dat hang\n5.2.1.1 Thiet ke he thong dat hang\nNoi dung thiet ke he thong dat hang invented.\n"
    },
    {
      "n": 19,
      "text": "6 Yeu cau co ban cho he thong cap do hai dat hang\n6.1 Yeu cau quan ly dat hang hai\n6.1.1 Thiet lap nhom chinh sach dat hang hai\n6.1.1.1 Chinh sach dat hang hai mot\nNoi dung chinh sach dat hang hai mot invented.\n6.1.1.2 Xay dung va cong bo dat hang hai\nNoi dung xay dung va cong bo dat hang hai invented.\n"
    },
    {
      "n": 67,
      "text": "Phu luc A\n(Quy dinh)\nYeu cau co ban ve an toan vat ly dat hang\nA.1 Yeu cau vat ly cho he thong cap do mot dat hang\nA.1.1 Kiem soat truy cap vat ly dat hang\nNoi dung kiem soat truy cap vat ly dat hang invented.\nA.1.2 Chong trom chong pha hoai dat hang\nNoi dung chong trom dat hang invented.\nA.2 Yeu cau vat ly cho he thong cap do hai dat hang\nA.2.1 Kiem soat truy cap vat ly dat hang hai\nNoi dung kiem soat truy cap dat hang hai invented.\nA.2.10 Bao ve dien tu dat hang\nNoi dung bao ve dien tu dat hang invented.\n"
    }
  ]
}`

func TestBuildTCVN11930Tree_Synthetic(t *testing.T) {
	tree, err := BuildTCVN11930Tree(json.RawMessage(syntheticTCVN), "tcvn11930", "2017")
	if err != nil {
		t.Fatalf("BuildTCVN11930Tree: %v", err)
	}

	// Title.
	if tree.Title != "TCVN 11930 2017" {
		t.Errorf("title=%q, want %q", tree.Title, "TCVN 11930 2017")
	}

	// Golden counts for the fixture, enumerated by hand from the structure above:
	//   clauses (intermediate): 5, 5.1, 5.1.1, 5.1.2, 5.2, 5.2.1,
	//                           6, 6.1, 6.1.1, A.1, A.2                 = 11
	//   controls (leaf):        5.1.1.1, 5.1.1.2, 5.1.2.1, 5.2.1.1,
	//                           6.1.1.1, 6.1.1.2,
	//                           A.1.1, A.1.2, A.2.1, A.2.10             = 10
	//   total rows                                                      = 21
	var clauses, controls int
	for _, c := range tree.Controls {
		switch c.Kind {
		case "clause":
			clauses++
		case "control":
			controls++
		default:
			t.Errorf("unexpected kind %q for %s", c.Kind, c.Citation)
		}
	}
	if len(tree.Controls) != 21 {
		t.Fatalf("total rows=%d, want 21; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}
	if clauses != 11 {
		t.Errorf("clauses=%d, want 11", clauses)
	}
	if controls != 10 {
		t.Errorf("controls=%d, want 10", controls)
	}

	byCite := make(map[string]ControlRow, len(tree.Controls))
	idxByCite := make(map[string]int, len(tree.Controls))
	for i, c := range tree.Controls {
		byCite[c.Citation] = c
		idxByCite[c.Citation] = i
	}

	// Front matter and false positives MUST be excluded.
	for _, bad := range []string{"1", "3", "3.1", "4", "06", "6"} {
		if bad == "6" {
			continue // 6 is a real level heading; keep it
		}
		if _, ok := byCite[bad]; ok {
			t.Errorf("citation %q should have been excluded (front matter / false positive)", bad)
		}
	}

	// Root level 5 → clause, no parent, neutral title.
	c5 := byCite["5"]
	if c5.Kind != "clause" {
		t.Errorf("5 kind=%q, want clause", c5.Kind)
	}
	if c5.ParentIdx != -1 {
		t.Errorf("5 parentIdx=%d, want -1 (root)", c5.ParentIdx)
	}
	if c5.Title != "Clause 5" {
		t.Errorf("5 title=%q, want 'Clause 5'", c5.Title)
	}
	if c5.CitationNorm != "5" {
		t.Errorf("5 citation_norm=%q, want 5", c5.CitationNorm)
	}
	if c5.TitleOriginal == nil || !strings.Contains(*c5.TitleOriginal, "cap do mot dat hang") {
		t.Errorf("5 title_original=%v, want verbatim heading", c5.TitleOriginal)
	}

	// Intermediate 5.1.1 → clause, parent is 5.1.
	c511 := byCite["5.1.1"]
	if c511.Kind != "clause" {
		t.Errorf("5.1.1 kind=%q, want clause", c511.Kind)
	}
	if c511.Title != "Clause 5.1.1" {
		t.Errorf("5.1.1 title=%q, want 'Clause 5.1.1'", c511.Title)
	}
	if c511.ParentIdx != idxByCite["5.1"] {
		t.Errorf("5.1.1 parentIdx=%d, want idx of 5.1 (%d)", c511.ParentIdx, idxByCite["5.1"])
	}
	// Intermediate nodes carry no body (headings are consecutive).
	if c511.Body != nil {
		t.Errorf("5.1.1 body=%q, want nil for a pure heading", *c511.Body)
	}

	// Leaf 5.1.1.1 → control, parent 5.1.1, verbatim heading + body.
	leaf := byCite["5.1.1.1"]
	if leaf.Kind != "control" {
		t.Errorf("5.1.1.1 kind=%q, want control", leaf.Kind)
	}
	if leaf.CitationNorm != "5.1.1.1" {
		t.Errorf("5.1.1.1 citation_norm=%q, want 5.1.1.1", leaf.CitationNorm)
	}
	if leaf.Title != "Control 5.1.1.1" {
		t.Errorf("5.1.1.1 title=%q, want 'Control 5.1.1.1'", leaf.Title)
	}
	if leaf.ParentIdx != idxByCite["5.1.1"] {
		t.Errorf("5.1.1.1 parentIdx=%d, want idx of 5.1.1", leaf.ParentIdx)
	}
	if leaf.TitleOriginal == nil || !strings.Contains(*leaf.TitleOriginal, "Chinh sach dat hang mot") {
		t.Errorf("5.1.1.1 title_original=%v, want verbatim heading", leaf.TitleOriginal)
	}
	if leaf.Body == nil || !strings.Contains(*leaf.Body, "chinh sach dat hang so mot") {
		t.Errorf("5.1.1.1 body missing verbatim body text: %v", leaf.Body)
	}

	// Leaf 5.1.1.2 body carries the a)/b) bullets and the '06 thang' text
	// (which must NOT have been split off as a clause).
	leaf2 := byCite["5.1.1.2"]
	if leaf2.Body == nil {
		t.Fatal("5.1.1.2 body is nil")
	}
	if !strings.Contains(*leaf2.Body, "a) Muc dat hang thu nhat") {
		t.Errorf("5.1.1.2 body missing a) bullet: %q", *leaf2.Body)
	}
	if !strings.Contains(*leaf2.Body, "06 thang dat hang") {
		t.Errorf("5.1.1.2 body missing the '06 thang' line: %q", *leaf2.Body)
	}

	// Body text must be free of page-header artifacts.
	if strings.Contains(*leaf2.Body, "TCVN 11930:2017") {
		t.Errorf("5.1.1.2 body leaked page header: %q", *leaf2.Body)
	}

	// Annex: A.1 → clause root; A.1.1 → control child; multi-digit A.2.10.
	a1 := byCite["A.1"]
	if a1.Kind != "clause" {
		t.Errorf("A.1 kind=%q, want clause", a1.Kind)
	}
	if a1.ParentIdx != -1 {
		t.Errorf("A.1 parentIdx=%d, want -1 (annex root)", a1.ParentIdx)
	}
	a11 := byCite["A.1.1"]
	if a11.Kind != "control" {
		t.Errorf("A.1.1 kind=%q, want control", a11.Kind)
	}
	if a11.ParentIdx != idxByCite["A.1"] {
		t.Errorf("A.1.1 parentIdx=%d, want idx of A.1", a11.ParentIdx)
	}
	if _, ok := byCite["A.2.10"]; !ok {
		t.Error("A.2.10 (multi-digit last segment) missing")
	}
	if byCite["A.2.10"].Kind != "control" {
		t.Errorf("A.2.10 kind=%q, want control", byCite["A.2.10"].Kind)
	}

	// Every parent index must reference an already-inserted row (parent before
	// child) so writeTree's index linking is valid.
	for i, c := range tree.Controls {
		if c.ParentIdx >= i {
			t.Errorf("%s parentIdx=%d not < own index %d", c.Citation, c.ParentIdx, i)
		}
	}
}

func TestBuildTCVN11930Tree_Empty(t *testing.T) {
	if _, err := BuildTCVN11930Tree(json.RawMessage(`{"pages":[]}`), "tcvn11930", "2017"); err == nil {
		t.Error("want error for empty capture, got nil")
	}
	if _, err := BuildTCVN11930Tree(json.RawMessage(`{"pages":[{"n":9,"text":"1 Pham vi khong co yeu cau"}]}`), "tcvn11930", "2017"); err == nil {
		t.Error("want error when no numbered requirement clauses are found, got nil")
	}
}
