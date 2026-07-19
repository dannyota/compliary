package eval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	results := []CaseResult{
		{
			Case:             Case{ID: "q1", ExpectedCitations: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}}},
			RecallAtK:        1.0,
			RecallHits:       1,
			RecallWant:       1,
			MRRAtK:           1.0,
			Rank:             1,
			CurrentPrecision: 1.0,
			HitsCurrent:      3,
			HitsTotal:        3,
			AbstainCorrect:   true,
		},
		{
			Case:             Case{ID: "q2", ExpectedCitations: []ExpectedCitation{{FrameworkCode: "iso27001", VersionLabel: "2022", CitationNorm: "A.5.1"}}},
			RecallAtK:        0.0,
			RecallHits:       0,
			RecallWant:       1,
			MRRAtK:           0.0,
			Rank:             0,
			CurrentPrecision: 1.0,
			HitsCurrent:      2,
			HitsTotal:        2,
			AbstainCorrect:   true,
		},
		{
			Case:           Case{ID: "q-abstain", ExpectAbstain: true},
			Abstained:      true,
			AbstainCorrect: true,
		},
	}

	agg := Summarize(results)
	if agg.Cases != 3 {
		t.Errorf("Cases = %d, want 3", agg.Cases)
	}
	if agg.RecallCases != 2 {
		t.Errorf("RecallCases = %d, want 2", agg.RecallCases)
	}
	// recall: 1/2 = 0.5
	if agg.RecallAtK != 0.5 {
		t.Errorf("RecallAtK = %v, want 0.5", agg.RecallAtK)
	}
	// MRR: (1.0 + 0.0) / 2 = 0.5
	if agg.MRRAtK != 0.5 {
		t.Errorf("MRRAtK = %v, want 0.5", agg.MRRAtK)
	}
	// current precision: 5/5 = 1.0
	if agg.CurrentPrecision != 1.0 {
		t.Errorf("CurrentPrecision = %v, want 1.0", agg.CurrentPrecision)
	}
	// abstain: 3/3 = 1.0
	if agg.AbstainAccuracy != 1.0 {
		t.Errorf("AbstainAccuracy = %v, want 1.0", agg.AbstainAccuracy)
	}
}

func TestThresholdsCheck(t *testing.T) {
	agg := Aggregate{
		Cases:            10,
		RecallAtK:        0.6,
		RecallCases:      8,
		MRRAtK:           0.5,
		MRRCases:         8,
		CurrentPrecision: 0.9,
		CurrentCases:     8,
		AbstainAccuracy:  0.8,
	}

	t.Run("all pass", func(t *testing.T) {
		th := Thresholds{MinRecall: 0.5, MinMRR: 0.4, MinCurrent: 0.8, MinAbstain: 0.7}
		fails := th.Check(agg)
		if len(fails) != 0 {
			t.Errorf("expected pass, got %d failures: %v", len(fails), fails)
		}
	})

	t.Run("recall fails", func(t *testing.T) {
		th := Thresholds{MinRecall: 0.9}
		fails := th.Check(agg)
		if len(fails) != 1 || fails[0].Metric != "recall@k" {
			t.Errorf("expected recall@k failure, got %v", fails)
		}
	})

	t.Run("zero threshold never fails", func(t *testing.T) {
		th := Thresholds{}
		fails := th.Check(agg)
		if len(fails) != 0 {
			t.Errorf("expected pass with zero thresholds, got %v", fails)
		}
	})
}

func TestWriteReport(t *testing.T) {
	results := []CaseResult{
		{
			Case:             Case{ID: "q1"},
			RecallHits:       1,
			RecallWant:       1,
			Rank:             1,
			CurrentPrecision: 1.0,
			AbstainCorrect:   true,
		},
	}
	agg := Summarize(results)
	var buf bytes.Buffer
	WriteReport(&buf, results, agg)
	out := buf.String()
	if !strings.Contains(out, "q1") {
		t.Error("report does not contain case id")
	}
	if !strings.Contains(out, "recall@k") {
		t.Error("report does not contain recall metric")
	}
}

func TestWriteJSONReport(t *testing.T) {
	results := []CaseResult{
		{
			Case:             Case{ID: "q1"},
			RecallHits:       1,
			RecallWant:       1,
			Rank:             1,
			CurrentPrecision: 1.0,
			AbstainCorrect:   true,
		},
	}
	agg := Summarize(results)
	meta := JSONReportMeta{
		RetrievalMode: "hybrid",
		TopK:          8,
		GeneratedAt:   "2026-07-19T00:00:00Z",
		Chunks:        3402,
	}
	var buf bytes.Buffer
	if err := WriteJSONReport(&buf, meta, results, agg); err != nil {
		t.Fatalf("WriteJSONReport: %v", err)
	}
	var report JSONReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if len(report.Cases) != 1 {
		t.Errorf("cases = %d, want 1", len(report.Cases))
	}
}

func TestLoadGoldenCSV(t *testing.T) {
	csv := `id,question,framework_code,version_label,citation_norm,expect_abstain,expect_fail,notes
q1,What is AC-2?,nist80053,r5,AC-2,false,false,test
q2,What is out of scope?,,,,true,false,abstain test
`
	cases, err := LoadGoldenEmbed([]byte(csv), "test.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("got %d cases, want 2", len(cases))
	}
	if cases[0].ID != "q1" || len(cases[0].ExpectedCitations) != 1 {
		t.Errorf("case 0: id=%s citations=%d", cases[0].ID, len(cases[0].ExpectedCitations))
	}
	if !cases[1].ExpectAbstain {
		t.Error("case 1 should be expect_abstain")
	}
}

func TestLoadGoldenCSVMultiCitation(t *testing.T) {
	csv := `id,question,framework_code,version_label,citation_norm,expect_abstain,expect_fail,notes
q1,What controls cover access?,nist80053,r5,AC-2,false,false,first citation
q1,What controls cover access?,nist80053,r5,AC-3,false,false,second citation
`
	cases, err := LoadGoldenEmbed([]byte(csv), "test.csv")
	if err != nil {
		t.Fatalf("LoadGoldenEmbed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("got %d cases, want 1 (merged)", len(cases))
	}
	if len(cases[0].ExpectedCitations) != 2 {
		t.Errorf("got %d citations, want 2", len(cases[0].ExpectedCitations))
	}
}

func TestLoadGoldenCSVRejectsEmptyID(t *testing.T) {
	csv := `id,question,framework_code,version_label,citation_norm,expect_abstain,expect_fail,notes
,What?,nist80053,r5,AC-2,false,false,
`
	_, err := LoadGoldenEmbed([]byte(csv), "test.csv")
	if err == nil {
		t.Error("expected error for empty id")
	}
}

func TestLoadGoldenCSVRejectsInScopeWithoutCitations(t *testing.T) {
	csv := `id,question,framework_code,version_label,citation_norm,expect_abstain,expect_fail,notes
q1,What?,,,,,false,false,
`
	_, err := LoadGoldenEmbed([]byte(csv), "test.csv")
	if err == nil {
		t.Error("expected error for in-scope case without citations")
	}
}
