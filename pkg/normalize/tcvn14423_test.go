package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

// syntheticTCVN14423 is a minimal txt-pages-json fixture that mirrors the
// STRUCTURAL shape of TCVN 14423:2025 without reproducing any licensed
// normative text.
//
// ALL topical Vietnamese wording is GENUINELY INVENTED placeholder content
// (an apiary / beekeeping theme that does not exist in the standard). It must
// never be copied, folded, or suffixed from real TCVN clause titles. Only
// generic boilerplate section names ("Pham vi ap dung", "Thuat ngu, dinh nghia
// va chu viet tat", "Chu viet tat") and the structural leaf/group labels
// ("Khai quat" / "Yeu cau cu the") are kept, because those carry no protected
// topical text — they are the standard's generic "Overview" / "Specific
// requirements" scaffold labels.
//
// Structure exercised (citation scheme 'tcvn-clause'):
//   - Front matter that MUST be excluded: section 1 (scope), a term definition
//     3.1/3.1.1, the 3.2 abbreviation heading and a " | "-separated abbrev row,
//     and table-of-contents entries (trailing tab + page number).
//   - A whole-line page header ("TCVN 14423:2025") that must be stripped from
//     bodies, while a mid-line self-reference survives.
//   - Section 4 (state-agency systems) written with SPACE separators and
//     section 5 (national-security systems) whose section/family headings use
//     TAB separators — both must parse.
//   - Family N.M -> clause; N.M.1 "Khai quat" -> leaf control (carries body);
//     N.M.2 "Yeu cau cu the" -> intermediate clause; N.M.2.x -> leaf controls.
//   - The real-document exception (mirrors 4.14.2): an N.M.2 group with NO
//     numbered children whose specific requirement is written inline as body —
//     it must resolve to a leaf control, not a clause.
//   - A leaf body carrying a) / b) bullets and a "03 lan" leading-number line
//     that must NOT be mistaken for a clause number.
const syntheticTCVN14423 = `{
  "pages": [
    {
      "n": 2,
      "text": "Muc luc\n4.1\tQuan ly to ong dat hang\t5\n4.9\tGia dinh khong ton tai dat hang\t9\n5.1\tGiam sat dan ong dat hang\t12\n"
    },
    {
      "n": 5,
      "text": "1 Pham vi ap dung dat hang\nNoi dung pham vi dat hang khong phai la yeu cau.\n3 Thuat ngu, dinh nghia va chu viet tat\n3.1 Thuat ngu va dinh nghia\n3.1.1\nThuat ngu trai ong dat hang (invented term)\nDinh nghia dat hang khong phai la yeu cau.\n3.2 Chu viet tat\nONG | To ong dat hang | Bee Hive Invented\n"
    },
    {
      "n": 6,
      "text": "4 Yeu cau doi voi trai ong dat hang\n4.1 Quan ly to ong dat hang\n4.1.1 Khai quat\nNoi dung khai quat quan ly to ong dat hang invented.\n4.1.2 Yeu cau cu the\n4.1.2.1 Thiet lap so theo doi to ong\nNoi dung so theo doi to ong dat hang invented.\n4.1.2.2 Kiem tra suc khoe dan ong\na) Muc kiem tra thu nhat cua yeu cau nay;\nb) Ghi nhan toi thieu 03 lan moi thang dat hang.\n"
    },
    {
      "n": 7,
      "text": "4.2 Quan ly mat ong dat hang\n4.2.1 Khai quat\nNoi dung khai quat mat ong dat hang invented.\n4.2.2 Yeu cau cu the\nNoi dung yeu cau cu the mat ong duoc viet truc tiep dat hang. Bao gom:\na) Lap danh sach thung mat ong dat hang.\nb) Cap nhat toi thieu 01 lan moi nam dat hang.\n"
    },
    {
      "n": 8,
      "text": "TCVN 14423:2025\n5\tYeu cau doi voi trai ong quan trong dat hang\n5.1\tGiam sat dan ong dat hang\n5.1.1 Khai quat\nNoi dung khai quat giam sat dan ong dat hang invented.\n5.1.2 Yeu cau cu the\n5.1.2.1 Trien khai cam bien nhiet do to ong\nNoi dung cam bien nhiet do dat hang invented.\nb) Tuan thu TCVN 14423:2025 khi ap dung dat hang.\n"
    }
  ]
}`

func TestBuildTCVN14423Tree_Synthetic(t *testing.T) {
	tree, err := BuildTCVN14423Tree(json.RawMessage(syntheticTCVN14423), "tcvn14423", "2025")
	if err != nil {
		t.Fatalf("BuildTCVN14423Tree: %v", err)
	}

	if tree.Title != "TCVN 14423 2025" {
		t.Errorf("title=%q, want %q", tree.Title, "TCVN 14423 2025")
	}

	// Golden counts for the fixture, enumerated by hand from the structure above:
	//   clauses (intermediate): 4, 4.1, 4.1.2, 4.2, 5, 5.1, 5.1.2      = 7
	//   controls (leaf):        4.1.1, 4.1.2.1, 4.1.2.2, 4.2.1, 4.2.2,
	//                           5.1.1, 5.1.2.1                          = 7
	//   total rows                                                      = 14
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
	if len(tree.Controls) != 14 {
		t.Fatalf("total rows=%d, want 14; got: %v", len(tree.Controls), controlIDs(tree.Controls))
	}
	if clauses != 7 {
		t.Errorf("clauses=%d, want 7", clauses)
	}
	if controls != 7 {
		t.Errorf("controls=%d, want 7", controls)
	}

	byCite := make(map[string]ControlRow, len(tree.Controls))
	idxByCite := make(map[string]int, len(tree.Controls))
	for i, c := range tree.Controls {
		byCite[c.Citation] = c
		idxByCite[c.Citation] = i
	}

	// Front matter and ToC artifacts MUST be excluded.
	for _, bad := range []string{"1", "3", "3.1", "3.1.1", "3.2", "4.9"} {
		if _, ok := byCite[bad]; ok {
			t.Errorf("citation %q should have been excluded (front matter / ToC artifact)", bad)
		}
	}

	// Section 4 root (space-separated heading) -> clause, no parent.
	c4 := byCite["4"]
	if c4.Kind != "clause" || c4.ParentIdx != -1 {
		t.Errorf("4: kind=%q parentIdx=%d, want clause / -1", c4.Kind, c4.ParentIdx)
	}
	if c4.Title != "Clause 4" || c4.CitationNorm != "4" {
		t.Errorf("4: title=%q citation_norm=%q, want 'Clause 4' / '4'", c4.Title, c4.CitationNorm)
	}
	if c4.TitleOriginal == nil || !strings.Contains(*c4.TitleOriginal, "trai ong dat hang") {
		t.Errorf("4: title_original=%v, want verbatim heading", c4.TitleOriginal)
	}

	// Family 4.1 -> clause; its ToC first-occurrence (with trailing page number)
	// must NOT have won: title_original carries no page-number tail.
	c41 := byCite["4.1"]
	if c41.Kind != "clause" || c41.ParentIdx != idxByCite["4"] {
		t.Errorf("4.1: kind=%q parentIdx=%d, want clause / idx(4)", c41.Kind, c41.ParentIdx)
	}
	if c41.TitleOriginal == nil || strings.ContainsAny(*c41.TitleOriginal, "0123456789") {
		t.Errorf("4.1: title_original=%v leaked a ToC page number", c41.TitleOriginal)
	}

	// N.M.1 "Khai quat" -> leaf control carrying verbatim body.
	c411 := byCite["4.1.1"]
	if c411.Kind != "control" || c411.ParentIdx != idxByCite["4.1"] {
		t.Errorf("4.1.1: kind=%q parentIdx=%d, want control / idx(4.1)", c411.Kind, c411.ParentIdx)
	}
	if c411.TitleOriginal == nil || *c411.TitleOriginal != "Khai quat" {
		t.Errorf("4.1.1: title_original=%v, want 'Khai quat'", c411.TitleOriginal)
	}
	if c411.Body == nil || !strings.Contains(*c411.Body, "khai quat quan ly to ong") {
		t.Errorf("4.1.1: body missing verbatim text: %v", c411.Body)
	}

	// N.M.2 "Yeu cau cu the" with children -> intermediate clause, no body.
	c412 := byCite["4.1.2"]
	if c412.Kind != "clause" {
		t.Errorf("4.1.2: kind=%q, want clause", c412.Kind)
	}
	if c412.Body != nil {
		t.Errorf("4.1.2: body=%q, want nil for a pure heading", *c412.Body)
	}

	// Leaf 4.1.2.2 body keeps a)/b) bullets and the "03 lan" line (must NOT have
	// been split off as a clause number).
	c4122 := byCite["4.1.2.2"]
	if c4122.Kind != "control" {
		t.Errorf("4.1.2.2: kind=%q, want control", c4122.Kind)
	}
	if c4122.Body == nil || !strings.Contains(*c4122.Body, "a) Muc kiem tra thu nhat") {
		t.Errorf("4.1.2.2: body missing a) bullet: %v", c4122.Body)
	}
	if c4122.Body == nil || !strings.Contains(*c4122.Body, "03 lan moi thang") {
		t.Errorf("4.1.2.2: body missing the '03 lan' line: %v", c4122.Body)
	}

	// Exception (mirrors 4.14.2): N.M.2 with no numbered children -> leaf control
	// whose specific requirement is inline body.
	c422 := byCite["4.2.2"]
	if c422.Kind != "control" {
		t.Errorf("4.2.2: kind=%q, want control (no numbered children)", c422.Kind)
	}
	if c422.ParentIdx != idxByCite["4.2"] {
		t.Errorf("4.2.2: parentIdx=%d, want idx(4.2)", c422.ParentIdx)
	}
	if c422.Body == nil || !strings.Contains(*c422.Body, "a) Lap danh sach thung mat ong") {
		t.Errorf("4.2.2: inline body missing: %v", c422.Body)
	}

	// Section 5 root uses a TAB separator -> must still parse as clause root.
	c5 := byCite["5"]
	if c5.Kind != "clause" || c5.ParentIdx != -1 {
		t.Errorf("5: kind=%q parentIdx=%d, want clause / -1", c5.Kind, c5.ParentIdx)
	}
	if c5.TitleOriginal == nil || !strings.Contains(*c5.TitleOriginal, "trai ong quan trong") {
		t.Errorf("5: title_original=%v, want verbatim heading (tab-separated)", c5.TitleOriginal)
	}

	// Tab-separated family 5.1 -> clause; leaf 5.1.2.1 present as control.
	if byCite["5.1"].Kind != "clause" {
		t.Errorf("5.1: kind=%q, want clause", byCite["5.1"].Kind)
	}
	if byCite["5.1.2.1"].Kind != "control" {
		t.Errorf("5.1.2.1: kind=%q, want control", byCite["5.1.2.1"].Kind)
	}

	// Parent-before-child ordering (writeTree index linking depends on it).
	for i, c := range tree.Controls {
		if c.ParentIdx >= i {
			t.Errorf("%s parentIdx=%d not < own index %d", c.Citation, c.ParentIdx, i)
		}
	}
}

// TestBuildTCVN14423Tree_AnchoredSkipFilters guards the anchored skip filters:
// a whole-line page header is stripped while a mid-line self-reference and a
// trailing-page-number-free requirement line survive; a ToC entry (trailing tab
// + page number) is stripped.
func TestBuildTCVN14423Tree_AnchoredSkipFilters(t *testing.T) {
	tree, err := BuildTCVN14423Tree(json.RawMessage(syntheticTCVN14423), "tcvn14423", "2025")
	if err != nil {
		t.Fatalf("BuildTCVN14423Tree: %v", err)
	}

	var leaf *ControlRow
	for i := range tree.Controls {
		if tree.Controls[i].Citation == "5.1.2.1" {
			leaf = &tree.Controls[i]
		}
	}
	if leaf == nil || leaf.Body == nil {
		t.Fatalf("5.1.2.1 control/body missing: %+v", leaf)
	}
	body := *leaf.Body

	// Mid-line "TCVN 14423:2025" self-reference inside a requirement survives.
	if !strings.Contains(body, "b) Tuan thu TCVN 14423:2025 khi ap dung") {
		t.Errorf("body dropped the TCVN 14423 self-reference line: %q", body)
	}
	// The whole-line running page header is stripped: the only remaining
	// occurrence is the mid-line self-reference.
	if n := strings.Count(body, "TCVN 14423:2025"); n != 1 {
		t.Errorf("TCVN 14423:2025 occurrences in body=%d, want 1 (header stripped, self-ref kept): %q", n, body)
	}

	// No body anywhere leaks a ToC entry (trailing tab + page number).
	for _, c := range tree.Controls {
		if c.Body != nil && strings.Contains(*c.Body, "Gia dinh khong ton tai") {
			t.Errorf("%s body leaked a ToC entry: %q", c.Citation, *c.Body)
		}
	}
}

// TestBuildTCVN14423Tree_CitationLikeBodyLines guards the verbatim-body
// invariant: a body line that classifies as a citation is dropped ONLY when it
// exactly reproduces its own heading line (a repeated running header). A wrapped
// bare-number line and a differing duplicate-citation line must survive.
func TestBuildTCVN14423Tree_CitationLikeBodyLines(t *testing.T) {
	// Section 5 must appear before the "5 nam" wrapped line so that line is a
	// duplicate-citation body line, not a new heading — mirroring the real
	// document, where no section-4/5 body line opens with a bare "4 "/"5 ".
	const fixture = `{
  "pages": [
    {
      "n": 8,
      "text": "5 Yeu cau doi voi trai ong quan trong dat hang\n5.1 Giam sat dan ong dat hang\n5.1.2 Yeu cau cu the\n5.1.2.1 So theo doi to ong\na) Thoi gian luu tru toi thieu la\n5 nam theo yeu cau nay dat hang.\n5.1.2.2 Phieu kiem tra to ong\na) Muc dau tien cua phieu;\n5.1.2.2 Bien ban thu hai khac tieu de\nb) Muc thu hai cua bien ban.\n5.1.2.1 So theo doi to ong\nc) Dong duplicate heading phia tren phai bi loai bo.\n"
    }
  ]
}`

	tree, err := BuildTCVN14423Tree(json.RawMessage(fixture), "tcvn14423", "2025")
	if err != nil {
		t.Fatalf("BuildTCVN14423Tree: %v", err)
	}

	byCite := make(map[string]ControlRow, len(tree.Controls))
	var controls int
	for _, c := range tree.Controls {
		byCite[c.Citation] = c
		if c.Kind == "control" {
			controls++
		}
	}

	// The colliding "5.1.2.2" must be a single control, not two.
	if controls != 2 {
		t.Errorf("controls=%d, want 2 (5.1.2.1 + one 5.1.2.2)", controls)
	}

	// Bare-number wrapped line survives in 5.1.2.1's body (section 5 already
	// seen, so "5 nam" is a differing duplicate-citation line, not a heading).
	c1 := byCite["5.1.2.1"]
	if c1.Body == nil || !strings.Contains(*c1.Body, "5 nam theo yeu cau nay dat hang.") {
		t.Errorf("5.1.2.1 body dropped the bare-number wrapped line: %v", c1.Body)
	}

	// Duplicate-citation line whose text differs is verbatim body content.
	c2 := byCite["5.1.2.2"]
	if c2.Body == nil {
		t.Fatal("5.1.2.2 body is nil")
	}
	if !strings.Contains(*c2.Body, "5.1.2.2 Bien ban thu hai khac tieu de") {
		t.Errorf("5.1.2.2 body dropped the differing duplicate-citation line: %q", *c2.Body)
	}
	if !strings.Contains(*c2.Body, "b) Muc thu hai cua bien ban.") {
		t.Errorf("5.1.2.2 body missing the second bullet: %q", *c2.Body)
	}
	// An EXACT reproduction of an earlier heading line is a running-header repeat
	// and must be dropped.
	if strings.Contains(*c2.Body, "So theo doi to ong") {
		t.Errorf("5.1.2.2 body leaked an exact heading-line repeat: %q", *c2.Body)
	}
}

func TestBuildTCVN14423Tree_Empty(t *testing.T) {
	if _, err := BuildTCVN14423Tree(json.RawMessage(`{"pages":[]}`), "tcvn14423", "2025"); err == nil {
		t.Error("want error for empty capture, got nil")
	}
	if _, err := BuildTCVN14423Tree(json.RawMessage(`{"pages":[{"n":5,"text":"1 Pham vi khong co yeu cau"}]}`), "tcvn14423", "2025"); err == nil {
		t.Error("want error when no numbered requirement clauses are found, got nil")
	}
}

func TestBuildTCVN14423Tree_WrongFramework(t *testing.T) {
	if _, err := BuildTCVN14423Tree(json.RawMessage(syntheticTCVN14423), "tcvn11930", "2017"); err == nil {
		t.Error("want error for wrong framework code, got nil")
	}
}
