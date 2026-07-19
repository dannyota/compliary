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

		// ccm-workbook: CSA CCM
		{"ccm-workbook", "AIS-01", []string{"ais-01"}},
		{"ccm-workbook", "DSP-17", []string{"dsp-17"}},
		{"ccm-workbook", "IAM-04", []string{"iam-04"}},

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
