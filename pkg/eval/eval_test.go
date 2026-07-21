package eval

import (
	"testing"
)

// hit builds a minimal Hit for testing metrics.
func hit(fw, ver, citNorm string) Hit {
	return Hit{FrameworkCode: fw, VersionLabel: ver, CitationNorm: citNorm, IsCurrent: true}
}

var m = Matcher{}

func TestRecall(t *testing.T) {
	tests := []struct {
		name      string
		expected  []ExpectedCitation
		hits      []Hit
		wantFrac  float64
		wantFound int
		wantWant  int
	}{
		{
			name:     "no expected citations (out of scope) has no denominator",
			expected: nil,
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantFrac: 0, wantFound: 0, wantWant: 0,
		},
		{
			name:     "exact match",
			expected: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}},
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantFrac: 1, wantFound: 1, wantWant: 1,
		},
		{
			name:     "case-insensitive match",
			expected: []ExpectedCitation{{FrameworkCode: "NIST80053", VersionLabel: "R5", CitationNorm: "ac-2"}},
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantFrac: 1, wantFound: 1, wantWant: 1,
		},
		{
			name:     "wrong framework misses",
			expected: []ExpectedCitation{{FrameworkCode: "iso27001", VersionLabel: "2022", CitationNorm: "A.5.1"}},
			hits:     []Hit{hit("iso27002", "2022", "A.5.1")},
			wantFrac: 0, wantFound: 0, wantWant: 1,
		},
		{
			name:     "wrong version misses",
			expected: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}},
			hits:     []Hit{hit("nist80053", "r4", "AC-2")},
			wantFrac: 0, wantFound: 0, wantWant: 1,
		},
		{
			name: "two expected one found",
			expected: []ExpectedCitation{
				{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"},
				{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-3"},
			},
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantFrac: 0.5, wantFound: 1, wantWant: 2,
		},
		{
			name:     "bare-id collision: same citation_norm different framework",
			expected: []ExpectedCitation{{FrameworkCode: "ciscontrols", VersionLabel: "v8.1", CitationNorm: "5.1"}},
			hits: []Hit{
				hit("iso27002", "2022", "5.1"),
				hit("ciscontrols", "v8.1", "5.1"),
			},
			wantFrac: 1, wantFound: 1, wantWant: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Case{ExpectedCitations: tt.expected}
			frac, found, want := Recall(c, tt.hits, m)
			if frac != tt.wantFrac || found != tt.wantFound || want != tt.wantWant {
				t.Errorf("Recall = (%v, %d, %d), want (%v, %d, %d)",
					frac, found, want, tt.wantFrac, tt.wantFound, tt.wantWant)
			}
		})
	}
}

func TestReciprocalRank(t *testing.T) {
	tests := []struct {
		name     string
		expected []ExpectedCitation
		hits     []Hit
		wantRR   float64
		wantRank int
	}{
		{
			name:     "no expected citations",
			expected: nil,
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantRR:   0, wantRank: 0,
		},
		{
			name:     "first hit",
			expected: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}},
			hits:     []Hit{hit("nist80053", "r5", "AC-2")},
			wantRR:   1, wantRank: 1,
		},
		{
			name:     "third hit",
			expected: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}},
			hits: []Hit{
				hit("nist80053", "r5", "AC-3"),
				hit("nist80053", "r5", "SI-7"),
				hit("nist80053", "r5", "AC-2"),
			},
			wantRR: 1.0 / 3.0, wantRank: 3,
		},
		{
			name:     "missing",
			expected: []ExpectedCitation{{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"}},
			hits:     []Hit{hit("nist80053", "r5", "AC-3")},
			wantRR:   0, wantRank: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRR, gotRank := ReciprocalRank(Case{ExpectedCitations: tt.expected}, tt.hits, m)
			if gotRR != tt.wantRR || gotRank != tt.wantRank {
				t.Errorf("ReciprocalRank = (%v, %d), want (%v, %d)", gotRR, gotRank, tt.wantRR, tt.wantRank)
			}
		})
	}
}

func TestCurrentPrecision(t *testing.T) {
	hits := []Hit{
		{DocumentID: 1, FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2", IsCurrent: true},
		{DocumentID: 2, FrameworkCode: "nist80053", VersionLabel: "r4", CitationNorm: "AC-2", IsCurrent: false},
		{DocumentID: 3, FrameworkCode: "iso27001", VersionLabel: "2022", CitationNorm: "A.5.1", IsCurrent: true},
	}

	t.Run("all current", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, func(h Hit) bool { return true }, nil)
		if frac != 1 || ok != 3 || total != 3 {
			t.Errorf("got (%v, %d, %d), want (1, 3, 3)", frac, ok, total)
		}
	})

	t.Run("non-current leak above current", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, func(h Hit) bool { return h.IsCurrent }, nil)
		want := 2.0 / 3.0
		if frac != want || ok != 2 || total != 3 {
			t.Errorf("got (%v, %d, %d), want (%v, 2, 3)", frac, ok, total, want)
		}
	})

	t.Run("trailing non-current excluded", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, func(h Hit) bool { return h.DocumentID == 1 }, nil)
		if frac != 1 || ok != 1 || total != 1 {
			t.Errorf("got (%v, %d, %d), want (1, 1, 1)", frac, ok, total)
		}
	})

	t.Run("nothing current scores over all", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, func(Hit) bool { return false }, nil)
		if frac != 0 || ok != 0 || total != 3 {
			t.Errorf("got (%v, %d, %d), want (0, 0, 3)", frac, ok, total)
		}
	})

	t.Run("no hits", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(nil, func(Hit) bool { return true }, nil)
		if frac != 0 || ok != 0 || total != 0 {
			t.Errorf("got (%v, %d, %d), want (0, 0, 0)", frac, ok, total)
		}
	})

	t.Run("nil predicate counts none current", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, nil, nil)
		if frac != 0 || ok != 0 || total != 3 {
			t.Errorf("got (%v, %d, %d), want (0, 0, 3)", frac, ok, total)
		}
	})

	t.Run("pinned version treats superseded hit as correct", func(t *testing.T) {
		// Simulate a version-pin case: iso27018:2019 is superseded (IsCurrent=false)
		// but the golden case explicitly requests it.
		pinHits := []Hit{
			{DocumentID: 1, FrameworkCode: "iso27018", VersionLabel: "2019", CitationNorm: "A.2.1", IsCurrent: false},
			{DocumentID: 2, FrameworkCode: "iso27018", VersionLabel: "2019", CitationNorm: "A.3.1", IsCurrent: false},
		}
		pinned := map[string]bool{"iso27018/2019": true}
		frac, ok, total := CurrentPrecision(pinHits, func(h Hit) bool { return h.IsCurrent }, pinned)
		if frac != 1 || ok != 2 || total != 2 {
			t.Errorf("got (%v, %d, %d), want (1, 2, 2)", frac, ok, total)
		}
	})

	t.Run("pinned version does not override unrelated hits", func(t *testing.T) {
		// A hit from a different framework should still be judged by isCurrent.
		mixHits := []Hit{
			{DocumentID: 1, FrameworkCode: "iso27018", VersionLabel: "2019", CitationNorm: "A.2.1", IsCurrent: false},
			{DocumentID: 2, FrameworkCode: "nist80053", VersionLabel: "r4", CitationNorm: "AC-2", IsCurrent: false},
		}
		pinned := map[string]bool{"iso27018/2019": true}
		// The nist80053/r4 hit is after the pinned one; it's not current and not
		// pinned, so it trails and gets excluded.
		frac, ok, total := CurrentPrecision(mixHits, func(h Hit) bool { return h.IsCurrent }, pinned)
		if frac != 1 || ok != 1 || total != 1 {
			t.Errorf("got (%v, %d, %d), want (1, 1, 1)", frac, ok, total)
		}
	})

	t.Run("no pin nil map behaves like original", func(t *testing.T) {
		frac, ok, total := CurrentPrecision(hits, func(h Hit) bool { return h.IsCurrent }, nil)
		want := 2.0 / 3.0
		if frac != want || ok != 2 || total != 3 {
			t.Errorf("got (%v, %d, %d), want (%v, 2, 3)", frac, ok, total, want)
		}
	})
}

func TestAbstainCorrect(t *testing.T) {
	tests := []struct {
		name          string
		expectAbstain bool
		abstained     bool
		want          bool
	}{
		{"in-scope answered correctly", false, false, true},
		{"in-scope wrongly abstained", false, true, false},
		{"out-of-scope correctly abstained", true, true, true},
		{"out-of-scope wrongly answered", true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Case{ExpectAbstain: tt.expectAbstain}
			if got := AbstainCorrect(c, tt.abstained); got != tt.want {
				t.Errorf("AbstainCorrect = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScore(t *testing.T) {
	c := Case{
		ID:       "q-test",
		Question: "What controls cover account management in NIST 800-53?",
		ExpectedCitations: []ExpectedCitation{
			{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2"},
			{FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-99"},
		},
	}
	hits := []Hit{
		{DocumentID: 1, FrameworkCode: "nist80053", VersionLabel: "r5", CitationNorm: "AC-2", IsCurrent: true},
		{DocumentID: 2, FrameworkCode: "nist80053", VersionLabel: "r4", CitationNorm: "AC-2", IsCurrent: false},
		{DocumentID: 3, FrameworkCode: "iso27001", VersionLabel: "2022", CitationNorm: "A.5.1", IsCurrent: true},
	}
	isCurrent := func(h Hit) bool { return h.IsCurrent }

	r := Score(c, hits, false, isCurrent, m)

	if r.RecallHits != 1 || r.RecallWant != 2 || r.RecallAtK != 0.5 {
		t.Errorf("recall = %d/%d (%v), want 1/2 (0.5)", r.RecallHits, r.RecallWant, r.RecallAtK)
	}
	if r.Rank != 1 || r.MRRAtK != 1 {
		t.Errorf("mrr = rank %d rr %v, want rank 1 rr 1", r.Rank, r.MRRAtK)
	}
	if r.HitsCurrent != 2 || r.HitsTotal != 3 {
		t.Errorf("current = %d/%d, want 2/3", r.HitsCurrent, r.HitsTotal)
	}
	if !r.AbstainCorrect {
		t.Error("AbstainCorrect = false, want true (in-scope, answered)")
	}
}

func TestMatcherBareIDCollision(t *testing.T) {
	// Citation "5.1" exists in CIS Controls, ISO 27002, ISO 27017, and SWIFT
	// CSCF. The matcher must disambiguate by framework+version.
	tests := []struct {
		name string
		ec   ExpectedCitation
		h    Hit
		want bool
	}{
		{
			name: "CIS 5.1 matches CIS",
			ec:   ExpectedCitation{FrameworkCode: "ciscontrols", VersionLabel: "v8.1", CitationNorm: "5.1"},
			h:    hit("ciscontrols", "v8.1", "5.1"),
			want: true,
		},
		{
			name: "CIS 5.1 does not match ISO 27002 5.1",
			ec:   ExpectedCitation{FrameworkCode: "ciscontrols", VersionLabel: "v8.1", CitationNorm: "5.1"},
			h:    hit("iso27002", "2022", "5.1"),
			want: false,
		},
		{
			name: "ISO 27002 5.1 matches ISO 27002",
			ec:   ExpectedCitation{FrameworkCode: "iso27002", VersionLabel: "2022", CitationNorm: "5.1"},
			h:    hit("iso27002", "2022", "5.1"),
			want: true,
		},
		{
			name: "SWIFT 5.1 does not match CIS 5.1",
			ec:   ExpectedCitation{FrameworkCode: "swiftcscf", VersionLabel: "v2026", CitationNorm: "5.1"},
			h:    hit("ciscontrols", "v8.1", "5.1"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Matches(tt.ec, tt.h)
			if got != tt.want {
				t.Errorf("Matcher.Matches = %v, want %v", got, tt.want)
			}
		})
	}
}
