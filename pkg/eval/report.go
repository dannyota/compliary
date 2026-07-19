package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Aggregate is the corpus-level roll-up of per-case results. RecallAtK and
// CurrentPrecision are micro-averages (sum of numerators / sum of denominators)
// so larger cases are not over- or under-weighted. MRRAtK is a macro-average
// (mean of per-case reciprocal ranks). Cases with no denominator for a metric
// are excluded, not counted as zero.
type Aggregate struct {
	Cases           int
	ExpectFailCases int
	GapPassCases    int

	RecallAtK   float64
	RecallCases int

	MRRAtK   float64
	MRRCases int

	CurrentPrecision float64
	CurrentCases     int

	AbstainAccuracy float64

	PoolRecall float64
	PoolCases  int
}

// Summarize folds per-case results into corpus metrics.
func Summarize(results []CaseResult) Aggregate {
	var agg Aggregate
	agg.Cases = len(results)

	var recallFound, recallWant int
	var currentOK, currentTotal int
	var abstainOK int
	var poolFound, poolWant int

	for _, r := range results {
		if r.Case.ExpectFail {
			agg.ExpectFailCases++
			if r.RecallWant > 0 && r.RecallHits == r.RecallWant {
				agg.GapPassCases++
			}
			continue
		}
		if r.RecallWant > 0 {
			recallFound += r.RecallHits
			recallWant += r.RecallWant
			agg.MRRAtK += r.MRRAtK
			agg.RecallCases++
			agg.MRRCases++
		}
		if r.PoolWant > 0 {
			poolFound += r.PoolHits
			poolWant += r.PoolWant
			agg.PoolCases++
		}
		if r.HitsTotal > 0 {
			currentOK += r.HitsCurrent
			currentTotal += r.HitsTotal
			agg.CurrentCases++
		}
		if r.AbstainCorrect {
			abstainOK++
		}
	}

	agg.RecallAtK = ratio(recallFound, recallWant)
	if agg.MRRCases > 0 {
		agg.MRRAtK /= float64(agg.MRRCases)
	}
	agg.CurrentPrecision = ratio(currentOK, currentTotal)
	agg.AbstainAccuracy = ratio(abstainOK, len(results)-agg.ExpectFailCases)
	agg.PoolRecall = ratio(poolFound, poolWant)
	return agg
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// Thresholds are the minimum aggregate metrics required to pass. A zero field
// imposes no floor.
type Thresholds struct {
	MinRecall  float64
	MinMRR     float64
	MinCurrent float64
	MinAbstain float64
}

// Failure is one threshold that the aggregate did not meet.
type Failure struct {
	Metric string
	Got    float64
	Want   float64
}

// Check returns the thresholds the aggregate failed to meet.
func (t Thresholds) Check(agg Aggregate) []Failure {
	var fails []Failure
	add := func(metric string, got, want float64, hasData bool) {
		if want > 0 && hasData && got < want {
			fails = append(fails, Failure{Metric: metric, Got: got, Want: want})
		}
	}
	add("recall@k", agg.RecallAtK, t.MinRecall, agg.RecallCases > 0)
	add("mrr@k", agg.MRRAtK, t.MinMRR, agg.MRRCases > 0)
	add("current-version-precision", agg.CurrentPrecision, t.MinCurrent, agg.CurrentCases > 0)
	add("abstention-accuracy", agg.AbstainAccuracy, t.MinAbstain, agg.Cases > 0)
	return fails
}

// WriteReport renders a human-readable per-case table plus the aggregate
// summary. Deterministic (cases in input order) so output diffs cleanly.
func WriteReport(w io.Writer, results []CaseResult, agg Aggregate) {
	_, _ = fmt.Fprintln(w, "ID                    ABSTAIN  RECALL@K   RANK  CURRENT   OK")
	_, _ = fmt.Fprintln(w, "--------------------  -------  ---------  ----  --------  --------")
	for _, r := range results {
		abst := boolMark(r.Abstained)
		okMark := passFail(r.AbstainCorrect)
		if r.Case.ExpectFail {
			okMark = "GAP"
			if r.RecallWant > 0 && r.RecallHits == r.RecallWant {
				okMark = "GAP-PASS"
			}
		}
		_, _ = fmt.Fprintf(w, "%-20s  %-7s  %4d/%-4d  %-4s  %5.0f%%     %s\n",
			truncate(r.Case.ID, 20),
			abst,
			r.RecallHits, r.RecallWant,
			rankMark(r.Rank),
			r.CurrentPrecision*100,
			okMark,
		)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Cases: %d", agg.Cases)
	if agg.ExpectFailCases > 0 {
		_, _ = fmt.Fprintf(w, " (%d known-gap excluded)", agg.ExpectFailCases)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "recall@k:                  %s\n", pct(agg.RecallAtK, agg.RecallCases))
	_, _ = fmt.Fprintf(w, "mrr@k:                     %s\n", pct(agg.MRRAtK, agg.MRRCases))
	_, _ = fmt.Fprintf(w, "current-version-precision: %s\n", pct(agg.CurrentPrecision, agg.CurrentCases))
	_, _ = fmt.Fprintf(w, "abstention-accuracy:       %s\n", pct(agg.AbstainAccuracy, agg.Cases))
	if agg.PoolCases > 0 {
		_, _ = fmt.Fprintf(w, "pool-recall:               %s\n", pct(agg.PoolRecall, agg.PoolCases))
	}

	if agg.GapPassCases > 0 {
		var ids []string
		for _, r := range results {
			if r.Case.ExpectFail && r.RecallWant > 0 && r.RecallHits == r.RecallWant {
				ids = append(ids, r.Case.ID)
			}
		}
		_, _ = fmt.Fprintf(w, "\n%d known-gap case(s) now pass -- consider removing expect_fail: %s\n",
			agg.GapPassCases, strings.Join(ids, ", "))
	}
}

func pct(v float64, cases int) string {
	if cases == 0 {
		return "n/a (0 cases)"
	}
	return fmt.Sprintf("%.1f%% (%d cases)", v*100, cases)
}

func boolMark(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func passFail(b bool) string {
	if b {
		return "OK"
	}
	return "XX"
}

func rankMark(rank int) string {
	if rank <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d", rank)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// --- JSON report artifact ---

// JSONReport is the schema for the machine-readable eval output file.
type JSONReport struct {
	SchemaVersion int              `json:"schema_version"`
	RetrievalMode string           `json:"retrieval_mode"`
	TopK          int              `json:"top_k"`
	PoolK         int              `json:"pool_k,omitempty"`
	DocCap        int              `json:"doc_cap,omitempty"`
	GeneratedAt   string           `json:"generated_at"`
	Corpus        JSONReportCorpus `json:"corpus"`
	Aggregate     JSONReportAgg    `json:"aggregate"`
	Cases         []JSONReportCase `json:"cases"`
}

type JSONReportCorpus struct {
	Chunks int64 `json:"chunks"`
}

type JSONReportAgg struct {
	RecallAtK        float64 `json:"recall_at_k"`
	RecallCases      int     `json:"recall_cases"`
	MRRAtK           float64 `json:"mrr_at_k"`
	MRRCases         int     `json:"mrr_cases"`
	CurrentPrecision float64 `json:"current_version_precision"`
	CurrentCases     int     `json:"current_version_cases"`
	AbstainAccuracy  float64 `json:"abstain_accuracy"`
	Cases            int     `json:"cases"`
	ExpectFailCases  int     `json:"expect_fail_cases"`
	GapPassCases     int     `json:"gap_pass_cases"`
	PoolRecall       float64 `json:"pool_recall,omitempty"`
	PoolCases        int     `json:"pool_cases,omitempty"`
}

type JSONReportCase struct {
	ID               string  `json:"id"`
	RecallHits       int     `json:"recall_hits"`
	RecallWant       int     `json:"recall_want"`
	Rank             int     `json:"rank"`
	CurrentPrecision float64 `json:"current_version_precision"`
	Abstained        bool    `json:"abstained"`
	AbstainCorrect   bool    `json:"abstain_correct"`
	ExpectFail       bool    `json:"expect_fail"`
	GapPass          bool    `json:"gap_pass"`
	PoolHits         int     `json:"pool_hits,omitempty"`
	PoolWant         int     `json:"pool_want,omitempty"`
	PoolRank         int     `json:"pool_rank,omitempty"`
}

// JSONReportMeta carries the non-metric metadata that cmd/eval supplies.
type JSONReportMeta struct {
	RetrievalMode string
	TopK          int
	PoolK         int
	DocCap        int
	GeneratedAt   string // RFC 3339
	Chunks        int64
}

// WriteJSONReport writes a machine-readable JSON eval report to w.
func WriteJSONReport(w io.Writer, meta JSONReportMeta, results []CaseResult, agg Aggregate) error {
	report := JSONReport{
		SchemaVersion: 1,
		RetrievalMode: meta.RetrievalMode,
		TopK:          meta.TopK,
		PoolK:         meta.PoolK,
		DocCap:        meta.DocCap,
		GeneratedAt:   meta.GeneratedAt,
		Corpus:        JSONReportCorpus{Chunks: meta.Chunks},
		Aggregate: JSONReportAgg{
			RecallAtK:        agg.RecallAtK,
			RecallCases:      agg.RecallCases,
			MRRAtK:           agg.MRRAtK,
			MRRCases:         agg.MRRCases,
			CurrentPrecision: agg.CurrentPrecision,
			CurrentCases:     agg.CurrentCases,
			AbstainAccuracy:  agg.AbstainAccuracy,
			Cases:            agg.Cases,
			ExpectFailCases:  agg.ExpectFailCases,
			GapPassCases:     agg.GapPassCases,
			PoolRecall:       agg.PoolRecall,
			PoolCases:        agg.PoolCases,
		},
	}
	report.Cases = make([]JSONReportCase, len(results))
	for i, r := range results {
		gapPass := r.Case.ExpectFail && r.RecallWant > 0 && r.RecallHits == r.RecallWant
		report.Cases[i] = JSONReportCase{
			ID:               r.Case.ID,
			RecallHits:       r.RecallHits,
			RecallWant:       r.RecallWant,
			Rank:             r.Rank,
			CurrentPrecision: r.CurrentPrecision,
			Abstained:        r.Abstained,
			AbstainCorrect:   r.AbstainCorrect,
			ExpectFail:       r.Case.ExpectFail,
			GapPass:          gapPass,
			PoolHits:         r.PoolHits,
			PoolWant:         r.PoolWant,
			PoolRank:         r.PoolRank,
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
