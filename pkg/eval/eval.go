package eval

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var errUnknownSearchMode = errors.New("unknown search mode")

// ExpectedCitation is one framework control a golden case expects the retriever
// to surface. FrameworkCode + VersionLabel + CitationNorm identify the control
// in silver.control. All matching is case-insensitive.
type ExpectedCitation struct {
	FrameworkCode string `json:"framework_code"`
	VersionLabel  string `json:"version_label"`
	CitationNorm  string `json:"citation_norm"`
}

// Case is one golden query with its expectations. ID is a stable identifier for
// the report. ExpectedCitations lists the controls a good retrieval should
// surface (empty for an out-of-scope question). ExpectAbstain is true when the
// correct behavior is to abstain (out of scope / not in the corpus). Notes is
// free-form context for humans, ignored by the metrics.
type Case struct {
	ID                string             `json:"id"`
	Question          string             `json:"question"`
	ExpectedCitations []ExpectedCitation `json:"expected_citations"`
	ExpectAbstain     bool               `json:"expect_abstain"`
	ExpectFail        bool               `json:"expect_fail,omitempty"`
	Notes             string             `json:"notes,omitempty"`
}

// CaseResult is the scored outcome of one Case. The metric fields are per-case;
// cmd/eval aggregates them. Counts (denominators) are kept so aggregation can
// be a true micro-average rather than a mean-of-means.
type CaseResult struct {
	Case      Case
	Abstained bool

	// Recall: fraction of ExpectedCitations found among the retrieved hits.
	RecallAtK  float64
	RecallHits int
	RecallWant int

	// MRR: reciprocal rank of the first expected citation found.
	MRRAtK float64
	Rank   int

	// CurrentPrecision: fraction of returned hits that are current-version.
	CurrentPrecision float64
	HitsCurrent      int
	HitsTotal        int

	// AbstainCorrect: answer abstention matched the case expectation.
	AbstainCorrect bool

	// Pool probe (optional).
	PoolHits int
	PoolWant int
	PoolRank int
}

// CurrentFn reports whether a retrieved hit's framework version is current.
// cmd/eval supplies a DB-backed implementation; tests pass a synthetic
// predicate.
type CurrentFn func(h Hit) bool

// Matcher holds the citation-matching logic for compliary. Unlike banhmi's
// jurisdiction-keyword matcher, compliary matches on the normalized citation
// string (citation_norm) and framework identity, which is simpler because
// citation_norm is globally unique within a framework+version.
type Matcher struct{}

// Matches reports whether a retrieved hit matches the expected citation:
// same framework_code, version_label, and citation_norm (all case-insensitive).
func (m Matcher) Matches(ec ExpectedCitation, h Hit) bool {
	if !strings.EqualFold(ec.FrameworkCode, h.FrameworkCode) {
		return false
	}
	if !strings.EqualFold(ec.VersionLabel, h.VersionLabel) {
		return false
	}
	if !strings.EqualFold(ec.CitationNorm, h.CitationNorm) {
		return false
	}
	return true
}

// MatchesAny reports whether a retrieved hit matches any expected citation.
func (m Matcher) MatchesAny(c Case, h Hit) bool {
	for _, ec := range c.ExpectedCitations {
		if m.Matches(ec, h) {
			return true
		}
	}
	return false
}

// Recall computes recall@k for one case: the fraction of expected citations
// found among the retrieved hits. Out-of-scope cases (no expected citations)
// return (0, 0, 0).
func Recall(c Case, hits []Hit, m Matcher) (frac float64, found, want int) {
	want = len(c.ExpectedCitations)
	if want == 0 {
		return 0, 0, 0
	}
	for _, ec := range c.ExpectedCitations {
		for _, h := range hits {
			if m.Matches(ec, h) {
				found++
				break
			}
		}
	}
	return float64(found) / float64(want), found, want
}

// ReciprocalRank computes reciprocal rank for one case: 1/rank of the first
// retrieved hit matching any expected citation. Out-of-scope cases return (0, 0).
func ReciprocalRank(c Case, hits []Hit, m Matcher) (rr float64, rank int) {
	if len(c.ExpectedCitations) == 0 {
		return 0, 0
	}
	for i, h := range hits {
		for _, ec := range c.ExpectedCitations {
			if m.Matches(ec, h) {
				rank = i + 1
				return 1.0 / float64(rank), rank
			}
		}
	}
	return 0, 0
}

// CurrentPrecision computes the fraction of returned hits that are
// current-version, excluding the trailing badged non-current run (evidence
// disclosure, not a leak). Any non-current hit ABOVE the last current hit
// counts against precision. No hits returns (0, 0, 0).
func CurrentPrecision(hits []Hit, isCurrent CurrentFn) (frac float64, ok, total int) {
	end := len(hits)
	for end > 0 && (isCurrent == nil || !isCurrent(hits[end-1])) {
		end--
	}
	if end == 0 && len(hits) > 0 {
		end = len(hits)
	}
	scored := hits[:end]
	total = len(scored)
	if total == 0 {
		return 0, 0, 0
	}
	for _, h := range scored {
		if isCurrent != nil && isCurrent(h) {
			ok++
		}
	}
	return float64(ok) / float64(total), ok, total
}

// AbstainCorrect reports whether the run's abstention matched the case's
// expectation.
func AbstainCorrect(c Case, abstained bool) bool {
	return abstained == c.ExpectAbstain
}

// Score runs every retrieval metric for one case and returns the combined
// CaseResult.
func Score(c Case, hits []Hit, abstained bool, isCurrent CurrentFn, m Matcher) CaseResult {
	r := CaseResult{Case: c, Abstained: abstained}
	r.RecallAtK, r.RecallHits, r.RecallWant = Recall(c, hits, m)
	r.MRRAtK, r.Rank = ReciprocalRank(c, hits, m)
	r.CurrentPrecision, r.HitsCurrent, r.HitsTotal = CurrentPrecision(hits, isCurrent)
	r.AbstainCorrect = AbstainCorrect(c, abstained)
	return r
}

// --- Golden set loading (CSV) ---

// goldenCSVColumns are the required CSV header columns, in order.
var goldenCSVColumns = []string{
	"id", "question", "framework_code", "version_label", "citation_norm",
	"expect_abstain", "expect_fail", "notes",
}

// LoadGolden reads and validates a golden CSV set from path. The CSV format is:
//
//	id,question,framework_code,version_label,citation_norm,expect_abstain,expect_fail,notes
//
// Multiple rows with the same id are merged into one Case with multiple
// ExpectedCitations. Abstain cases have empty framework_code/version_label/
// citation_norm.
func LoadGolden(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read golden set %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseGoldenCSV(f, path)
}

// LoadGoldenEmbed loads golden CSV from embedded bytes.
func LoadGoldenEmbed(b []byte, src string) ([]Case, error) {
	return parseGoldenCSV(strings.NewReader(string(b)), src)
}

func parseGoldenCSV(r io.Reader, src string) ([]Case, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = len(goldenCSVColumns)
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("golden set %s: read header: %w", src, err)
	}
	for i, col := range goldenCSVColumns {
		if i >= len(header) || strings.TrimSpace(header[i]) != col {
			return nil, fmt.Errorf("golden set %s: expected column %d to be %q, got %q",
				src, i, col, safeIndex(header, i))
		}
	}

	type caseKey struct {
		id            string
		question      string
		expectAbstain bool
		expectFail    bool
		notes         string
	}

	order := make([]string, 0) // preserve first-seen order
	cases := make(map[string]*caseKey)
	citations := make(map[string][]ExpectedCitation)

	for line := 2; ; line++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("golden set %s: line %d: %w", src, line, err)
		}

		id := strings.TrimSpace(row[0])
		question := strings.TrimSpace(row[1])
		fwCode := strings.TrimSpace(row[2])
		verLabel := strings.TrimSpace(row[3])
		citNorm := strings.TrimSpace(row[4])
		abstain := strings.TrimSpace(row[5])
		fail := strings.TrimSpace(row[6])
		notes := strings.TrimSpace(row[7])

		if id == "" {
			return nil, fmt.Errorf("golden set %s: line %d: empty id", src, line)
		}
		if question == "" {
			return nil, fmt.Errorf("golden set %s: line %d (%s): empty question", src, line, id)
		}

		isAbstain := abstain == "true" || abstain == "1"
		isFail := fail == "true" || fail == "1"

		if _, seen := cases[id]; !seen {
			cases[id] = &caseKey{
				id:            id,
				question:      question,
				expectAbstain: isAbstain,
				expectFail:    isFail,
				notes:         notes,
			}
			order = append(order, id)
		}

		if fwCode != "" && citNorm != "" {
			citations[id] = append(citations[id], ExpectedCitation{
				FrameworkCode: fwCode,
				VersionLabel:  verLabel,
				CitationNorm:  citNorm,
			})
		}
	}

	if len(order) == 0 {
		return nil, fmt.Errorf("golden set %s is empty", src)
	}

	result := make([]Case, 0, len(order))
	for _, id := range order {
		ck := cases[id]
		c := Case{
			ID:                ck.id,
			Question:          ck.question,
			ExpectedCitations: citations[id],
			ExpectAbstain:     ck.expectAbstain,
			ExpectFail:        ck.expectFail,
			Notes:             ck.notes,
		}
		if !c.ExpectAbstain && len(c.ExpectedCitations) == 0 {
			return nil, fmt.Errorf("golden set %s: in-scope case %q has no expected_citations "+
				"(set expect_abstain or add citations)", src, c.ID)
		}
		for _, ec := range c.ExpectedCitations {
			if ec.FrameworkCode == "" {
				return nil, fmt.Errorf("golden set %s: case %q has citation with empty framework_code", src, c.ID)
			}
			if ec.CitationNorm == "" {
				return nil, fmt.Errorf("golden set %s: case %q has citation with empty citation_norm", src, c.ID)
			}
		}
		result = append(result, c)
	}
	return result, nil
}

func safeIndex(ss []string, i int) string {
	if i >= len(ss) {
		return "<missing>"
	}
	return ss[i]
}
