package lexical

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTokenize_plainEnglish(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple words", "Information Security", []string{"information", "security"}},
		{"mixed case", "Access Control Policy", []string{"access", "control", "policy"}},
		{"digits only", "123 456", []string{"123", "456"}},
		{"empty", "", nil},
		{"punctuation splits", "risk-based approach", []string{"risk", "based", "approach"}},
		{"parenthetical plain", "information (security)", []string{"information", "security"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestTokenize_citationSchemes verifies that citation tokens from every supported
// framework scheme stay intact during tokenization.
func TestTokenize_citationSchemes(t *testing.T) {
	cases := []struct {
		scheme string
		input  string
		want   []string
	}{
		// oscal-catalog: NIST SP 800-53
		{"oscal-catalog", "AC-2", []string{"ac-2"}},
		{"oscal-catalog", "AC-2(3)", []string{"ac-2(3)"}},
		{"oscal-catalog", "SA-11(8)", []string{"sa-11(8)"}},
		{"oscal-catalog", "AC-2 Account Management", []string{"ac-2", "account", "management"}},

		// iso-ams: ISO 27001/27002 Annex A
		{"iso-ams", "A.5.1", []string{"a.5.1"}},
		{"iso-ams", "A.8.24", []string{"a.8.24"}},

		// iso-control-catalog: ISO 27002 body controls
		{"iso-control-catalog", "5.1", []string{"5.1"}},
		{"iso-control-catalog", "8.24", []string{"8.24"}},
		{"iso-control-catalog", "CLD.12.1.5", []string{"cld.12.1.5"}},
		{"iso-control-catalog", "CLD.12.4.1", []string{"cld.12.4.1"}},

		// tsc-criteria: SOC 2 / AICPA TSC
		{"tsc-criteria", "CC6.1", []string{"cc6.1"}},
		{"tsc-criteria", "CC7.2", []string{"cc7.2"}},
		{"tsc-criteria", "A1.1", []string{"a1.1"}},
		{"tsc-criteria", "PI1.1", []string{"pi1.1"}},

		// pci-requirement: PCI DSS
		{"pci-requirement", "Req 8.3.6", []string{"req", "8.3.6"}},
		{"pci-requirement", "1.2.1", []string{"1.2.1"}},
		{"pci-requirement", "12.3.4", []string{"12.3.4"}},

		// csf-workbook: NIST CSF 2.0
		{"csf-workbook", "PR.AA-01", []string{"pr.aa-01"}},
		{"csf-workbook", "GV.OC-02", []string{"gv.oc-02"}},
		{"csf-workbook", "ID.AM-08", []string{"id.am-08"}},

		// cis-workbook: CIS Controls
		{"cis-workbook", "4.1", []string{"4.1"}},
		{"cis-workbook", "16.12", []string{"16.12"}},
		{"cis-workbook", "1.1.1", []string{"1.1.1"}},

		// cscf-control: SWIFT CSCF
		{"cscf-control", "1.1", []string{"1.1"}},
		{"cscf-control", "2.8A", []string{"2.8a"}},
		{"cscf-control", "3.1", []string{"3.1"}},

		// ccm-workbook: CSA CCM (includes ampersand domains)
		{"ccm-workbook", "AIS-01", []string{"ais-01"}},
		{"ccm-workbook", "DSP-17", []string{"dsp-17"}},
		{"ccm-workbook", "IAM-04", []string{"iam-04"}},
		{"ccm-workbook", "A&A-01", []string{"a&a-01"}},
		{"ccm-workbook", "I&S-05", []string{"i&s-05"}},

		// tsc-criteria: SOC 2 Points of Focus (POF) sub-criteria
		{"tsc-criteria-pof", "A1.1-POF-01", []string{"a1.1-pof-01"}},

		// cobit-objective: COBIT
		{"cobit-objective", "EDM01.01", []string{"edm01.01"}},
		{"cobit-objective", "APO12.03", []string{"apo12.03"}},
	}
	for _, tc := range cases {
		t.Run(tc.scheme+"/"+tc.input, func(t *testing.T) {
			got := Tokenize(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestTokenize_citationInContext verifies citations are kept intact when surrounded by other words.
func TestTokenize_citationInContext(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // the citation token that must appear
	}{
		{"NIST in sentence", "Control AC-2(3) requires multi-factor", "ac-2(3)"},
		{"ISO annex ref", "see A.5.1 for details", "a.5.1"},
		{"CSF function", "function PR.AA-01 covers", "pr.aa-01"},
		{"COBIT in text", "objective EDM01.01 governs", "edm01.01"},
		{"CCM ref", "domain AIS-01 addresses", "ais-01"},
		{"PCI deep", "requirement 8.3.6 mandates", "8.3.6"},
		{"CLD prefix", "control CLD.12.1.5 specifies", "cld.12.1.5"},
		{"CCM ampersand", "domain A&A-01 covers audit", "a&a-01"},
		{"CCM ampersand I&S", "control I&S-05 addresses", "i&s-05"},
		{"TSC POF", "point of focus A1.1-POF-01 defines", "a1.1-pof-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := Tokenize(tc.input)
			found := false
			for _, tok := range tokens {
				if tok == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Tokenize(%q) = %v, missing expected citation token %q", tc.input, tokens, tc.want)
			}
		})
	}
}

// TestTokenize_trailingDot verifies sentence-final dots do not stick to citations.
func TestTokenize_trailingDot(t *testing.T) {
	got := Tokenize("See AC-2.")
	want := []string{"see", "ac-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize(%q) = %v, want %v", "See AC-2.", got, want)
	}
}

// TestTokenize_numericOnly verifies that pure numeric "citations" like 5.1 are
// still kept as single tokens (they match the citation pattern: digit + dot).
func TestTokenize_numericCitation(t *testing.T) {
	got := Tokenize("5.1")
	want := []string{"5.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize(%q) = %v, want %v", "5.1", got, want)
	}
}

// TestIsCitationToken covers edge cases.
func TestIsCitationToken(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"ac-2", true},         // letter + digit
		{"ac-2(3)", true},      // letter + digit + parens
		{"a.5.1", true},        // letter + dot + digit
		{"5.1", true},          // digit + dot = numeric citation (CIS, ISO, PCI, SWIFT)
		{"information", false}, // pure letters, no digit/dot
		{"123", false},         // pure digits
		{"pr.aa-01", true},     // letter + dot + digit
		{"edm01.01", true},     // letter + digit + dot
		{"cld.12.1.5", true},   // letter + dot + digit
		{"a&a-01", true},       // letter + ampersand + digit (CCM)
		{"i&s-05", true},       // letter + ampersand + digit (CCM)
		{"a1.1-pof-01", true},  // letter + dot + digit (TSC POF)
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := isCitationToken(tc.input); got != tc.want {
				t.Errorf("isCitationToken(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// parseSparse parses "{id:val,...}/dim" into a map for scoring in tests.
func parseSparse(t *testing.T, lit string) map[int32]float64 {
	t.Helper()
	body := lit[strings.IndexByte(lit, '{')+1 : strings.IndexByte(lit, '}')]
	out := map[int32]float64{}
	if body == "" {
		return out
	}
	for _, kv := range strings.Split(body, ",") {
		parts := strings.SplitN(kv, ":", 2)
		id, _ := strconv.Atoi(parts[0])
		v, _ := strconv.ParseFloat(parts[1], 64)
		out[int32(id)] = v
	}
	return out
}

func dot(q, d map[int32]float64) float64 {
	var s float64
	for id, qv := range q {
		s += qv * d[id]
	}
	return s
}

// TestBM25Ranking: dot(query, doc) == BM25 score, so an on-topic doc must
// outscore an off-topic one, and a rare term must outweigh common-term spam.
func TestBM25Ranking(t *testing.T) {
	corpus := []string{
		"AC-2 Account Management: manage information system accounts",
		"PR.AA-01 Identity management, authentication, and access control",
		"photosynthesis in tropical plant biology and rainforest ecosystems",
		"account account account management management management",
	}
	e := Train(corpus)
	q := parseSparse(t, QueryVector("AC-2 account management"))

	score := func(text string) float64 { return dot(q, parseSparse(t, e.DocVector(text))) }
	onTopic := score(corpus[0])
	offTopic := score(corpus[2]) // plants
	if onTopic <= offTopic {
		t.Fatalf("on-topic %.3f should outscore off-topic %.3f", onTopic, offTopic)
	}
	if offTopic != 0 {
		t.Fatalf("fully off-topic doc should score 0, got %.3f", offTopic)
	}
	// A doc spamming common terms must not beat the doc with the specific citation.
	if spam := score(corpus[3]); spam >= onTopic {
		t.Fatalf("common-term spam %.3f should not beat the relevant doc %.3f", spam, onTopic)
	}
}

// TestQueryVector_citationHits verifies that querying a citation produces a
// vector that scores non-zero against a doc containing that citation.
func TestQueryVector_citationHits(t *testing.T) {
	corpus := []string{
		"AC-2(3) Additional Authenticator Management",
		"CC6.1 Logical and Physical Access Controls",
		"PR.AA-01 Identity Management",
	}
	e := Train(corpus)

	citations := []struct {
		query  string
		target int // index in corpus that should score > 0
	}{
		{"AC-2(3)", 0},
		{"CC6.1", 1},
		{"PR.AA-01", 2},
	}
	for _, c := range citations {
		t.Run(c.query, func(t *testing.T) {
			q := parseSparse(t, QueryVector(c.query))
			d := parseSparse(t, e.DocVector(corpus[c.target]))
			s := dot(q, d)
			if s <= 0 {
				t.Errorf("query %q should score >0 against corpus[%d], got %.3f", c.query, c.target, s)
			}
		})
	}
}

// activeDims returns the set of hash dimensions present in a query vector.
func activeDims(t *testing.T, query string) map[int32]bool {
	t.Helper()
	out := map[int32]bool{}
	for id := range parseSparse(t, QueryVector(query)) {
		out[id] = true
	}
	return out
}

// TestQueryVector_vietnameseStopwords verifies that Vietnamese function
// syllables are dropped from the query vector while domain-bearing syllables
// survive. The tokenizer splits on whitespace/punctuation only, so every
// Vietnamese syllable is its own token: a stopword entry filters exactly one
// syllable and can never remove a whole multi-syllable term.
func TestQueryVector_vietnameseStopwords(t *testing.T) {
	// Function syllables: grammatical words in every context of this corpus's
	// register, so each must produce an empty query vector on its own.
	filtered := []string{
		"và", "hoặc", "nếu",
		"các", "những", "này", "đó",
		"của", "trong", "trên", "với", "cho", "về", "để", "khi",
		"là", "được",
		"nào", "gì",
	}
	for _, w := range filtered {
		t.Run("filtered/"+w, func(t *testing.T) {
			if got := activeDims(t, w); len(got) != 0 {
				t.Errorf("stopword %q should produce an empty query vector, got %d dims", w, len(got))
			}
		})
	}

	// Domain-bearing syllables that must survive: security compounds
	// (an toàn, bảo mật, mật khẩu, hệ thống, máy chủ, sao lưu, theo dõi,
	// từ xa, ra vào) and the collision-risk words deliberately NOT listed.
	kept := []string{
		"toàn", "ninh", "bảo", "mật", "khẩu", "hệ", "thống", "yêu", "cầu", "máy", "chủ",
		"có", "không", "phải", "theo", "dõi", "đối", "bằng", "gồm", "bao",
		"một", "vào", "ra", "từ", "sao", "lưu", "tại", "ai",
	}
	for _, w := range kept {
		t.Run("kept/"+w, func(t *testing.T) {
			if len(activeDims(t, w)) == 0 {
				t.Errorf("domain syllable %q must survive query filtering", w)
			}
		})
	}
}

// TestQueryVector_vietnameseDiacritics guards the tone-mark boundary: the
// tokenizer lowercases but never folds diacritics, so a stopword must never
// swallow its differently-toned domain twin (về/vệ, là/lý, các/cách).
func TestQueryVector_vietnameseDiacritics(t *testing.T) {
	pairs := []struct{ stopword, domain string }{
		{"về", "vệ"},    // về = about; vệ = bảo vệ (protect)
		{"là", "lý"},    // là = copula; lý = quản lý (manage)
		{"các", "cách"}, // các = plural marker; cách = biện pháp/cách thức (method)
		{"cho", "chủ"},  // cho = for; chủ = máy chủ (server)
	}
	for _, p := range pairs {
		t.Run(p.stopword+"/"+p.domain, func(t *testing.T) {
			if len(activeDims(t, p.stopword)) != 0 {
				t.Errorf("%q should be filtered", p.stopword)
			}
			if len(activeDims(t, p.domain)) == 0 {
				t.Errorf("%q must survive — it is not the stopword %q", p.domain, p.stopword)
			}
		})
	}
}

// TestQueryVector_vietnameseGoldenQueries walks the shape of the Vietnamese
// golden-set questions: function words drop, every domain syllable stays.
func TestQueryVector_vietnameseGoldenQueries(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want []string // domain tokens that must remain
		drop []string // stopwords that must be gone
	}{
		{
			name: "sdlc",
			q:    "Yêu cầu về quy trình phát triển phần mềm an toàn theo vòng đời SDLC?",
			want: []string{"yêu", "cầu", "quy", "trình", "phát", "triển", "phần", "mềm", "toàn", "theo", "vòng", "đời", "sdlc"},
			drop: []string{"về"},
		},
		{
			name: "server-room-access",
			q:    "Kiểm soát ra vào phòng máy chủ với xác thực hai yếu tố?",
			want: []string{"kiểm", "soát", "ra", "vào", "phòng", "máy", "chủ", "xác", "thực", "hai", "yếu", "tố"},
			drop: []string{"với"},
		},
		{
			name: "log-retention",
			q:    "Quy định thu thập và lưu trữ nhật ký an ninh mạng gồm những gì?",
			want: []string{"quy", "định", "thu", "thập", "lưu", "trữ", "nhật", "ký", "ninh", "mạng", "gồm"},
			drop: []string{"và", "những", "gì"},
		},
		{
			name: "server-room-siting",
			q:    "Quy định về vị trí bố trí đặt phòng máy chủ trong tòa nhà?",
			want: []string{"quy", "định", "vị", "trí", "bố", "đặt", "phòng", "máy", "chủ", "tòa", "nhà"},
			drop: []string{"về", "trong"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := activeDims(t, tc.q)
			for _, w := range tc.want {
				if !got[termID(w)] {
					t.Errorf("query %q lost domain token %q", tc.name, w)
				}
			}
			for _, w := range tc.drop {
				if got[termID(w)] {
					t.Errorf("query %q retained stopword %q", tc.name, w)
				}
			}
		})
	}
}

// TestQueryVector_vietnameseAnCollision pins a deliberate cross-language
// collision. Vietnamese "an" is domain-bearing (an toàn = safety, an ninh =
// security) but is spelled exactly like the English article "an", already in
// the stopword list — and the corpus is majority English, so "an" is the least
// discriminative token available and would otherwise pull English chunks into
// Vietnamese results. It stays filtered; the second syllable (toàn / ninh) is
// Vietnamese-only and carries the phrase's retrieval signal.
func TestQueryVector_vietnameseAnCollision(t *testing.T) {
	if len(activeDims(t, "an")) != 0 {
		t.Error(`"an" must stay filtered — English article and Vietnamese syllable share the token`)
	}
	got := activeDims(t, "an toàn thông tin và an ninh mạng")
	for _, w := range []string{"toàn", "thông", "tin", "ninh", "mạng"} {
		if !got[termID(w)] {
			t.Errorf("security phrase lost its carrying syllable %q", w)
		}
	}
	if got[termID("an")] {
		t.Error(`"an" leaked into the query vector`)
	}
}

// TestQueryVector_englishUnchanged pins the pre-existing English stopword
// behavior so the Vietnamese additions cannot regress it.
func TestQueryVector_englishUnchanged(t *testing.T) {
	for _, w := range []string{"what", "how", "which", "does", "the", "for", "and", "are",
		"is", "of", "that", "a", "to", "in", "an", "control", "requirement", "cover", "address"} {
		t.Run("filtered/"+w, func(t *testing.T) {
			if len(activeDims(t, w)) != 0 {
				t.Errorf("english stopword %q should be filtered", w)
			}
		})
	}
	for _, w := range []string{"access", "encryption", "authentication", "firewall", "logging", "backup"} {
		t.Run("kept/"+w, func(t *testing.T) {
			if len(activeDims(t, w)) == 0 {
				t.Errorf("english content word %q must survive", w)
			}
		})
	}
}

// TestSparseLiteral_format verifies the pgvector literal format.
func TestSparseLiteral_format(t *testing.T) {
	got := sparseLiteral(map[int32]float64{3: 1.5, 1: 2.0})
	// Indices must be sorted ascending.
	if !strings.HasPrefix(got, "{1:") {
		t.Errorf("sparse literal not sorted: %s", got)
	}
	if !strings.HasSuffix(got, "/1048576") {
		t.Errorf("sparse literal missing /Dim suffix: %s", got)
	}
}

// TestDim verifies the hashing dimension constant.
func TestDim(t *testing.T) {
	if Dim != 1048576 {
		t.Errorf("Dim = %d, want 1048576 (2^20)", Dim)
	}
}
