package retrieve

import (
	"strings"
	"testing"

	"danny.vn/compliary/pkg/eval"
)

func TestCapPerFramework(t *testing.T) {
	hits := []eval.Hit{
		{ChunkID: 1, FrameworkCode: "nist80053"},
		{ChunkID: 2, FrameworkCode: "nist80053"},
		{ChunkID: 3, FrameworkCode: "nist80053"},
		{ChunkID: 4, FrameworkCode: "iso27001"},
		{ChunkID: 5, FrameworkCode: "iso27001"},
		{ChunkID: 6, FrameworkCode: "pcidss"},
	}

	// Cap at 2 per framework, topK=5.
	got := capPerFramework(hits, 2, 5)
	if len(got) != 5 {
		t.Fatalf("expected 5 hits, got %d", len(got))
	}
	// First 2 nist, then iso (2 fits), then pcidss (1).
	fwCounts := make(map[string]int)
	for _, h := range got {
		fwCounts[h.FrameworkCode]++
	}
	if fwCounts["nist80053"] != 2 {
		t.Errorf("nist80053: want 2, got %d", fwCounts["nist80053"])
	}
	if fwCounts["iso27001"] != 2 {
		t.Errorf("iso27001: want 2, got %d", fwCounts["iso27001"])
	}
	if fwCounts["pcidss"] != 1 {
		t.Errorf("pcidss: want 1, got %d", fwCounts["pcidss"])
	}
}

func TestCapPerFramework_BackfillDemoted(t *testing.T) {
	hits := []eval.Hit{
		{ChunkID: 1, FrameworkCode: "nist80053"},
		{ChunkID: 2, FrameworkCode: "nist80053"},
		{ChunkID: 3, FrameworkCode: "nist80053"},
	}
	// Cap=1, topK=3: first nist takes 1 slot, next 2 demoted, backfill fills to 3.
	got := capPerFramework(hits, 1, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 (backfilled), got %d", len(got))
	}
}

func TestCapPerFramework_NoCap(t *testing.T) {
	hits := []eval.Hit{
		{ChunkID: 1, FrameworkCode: "a"},
		{ChunkID: 2, FrameworkCode: "a"},
	}
	// docCap=0 means no capping — this function shouldn't be called with 0,
	// but if it is, it should truncate to topK.
	got := capPerFramework(hits, 0, 1)
	// All demoted (cap=0), backfill fills 1.
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
}

func TestMergePinned(t *testing.T) {
	pinned := []eval.Hit{
		{ChunkID: 100, Score: 1.0},
	}
	fused := []eval.Hit{
		{ChunkID: 100, Score: 0.03}, // duplicate of pinned
		{ChunkID: 200, Score: 0.02},
		{ChunkID: 300, Score: 0.01},
	}
	got := mergePinned(pinned, fused, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if got[0].ChunkID != 100 {
		t.Errorf("first should be pinned chunk 100, got %d", got[0].ChunkID)
	}
	if got[0].Score != 1.0 {
		t.Errorf("pinned should keep score 1.0, got %f", got[0].Score)
	}
	// Duplicate 100 should be skipped.
	for i, h := range got {
		if i > 0 && h.ChunkID == 100 {
			t.Error("duplicate pinned chunk not removed")
		}
	}
}

func TestAppendNonCurrent(t *testing.T) {
	current := []eval.Hit{{ChunkID: 1}, {ChunkID: 2}}
	nc := []eval.Hit{{ChunkID: 2}, {ChunkID: 3}} // chunk 2 is duplicate
	got := appendNonCurrent(current, nc)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// Chunk 3 should be appended, chunk 2 deduplicated.
	if got[2].ChunkID != 3 {
		t.Errorf("expected chunk 3 appended, got %d", got[2].ChunkID)
	}
}

func TestBestHitPerFramework(t *testing.T) {
	hits := []eval.Hit{
		{ChunkID: 1, FrameworkCode: "nist80053", VersionLabel: "r5"},
		{ChunkID: 2, FrameworkCode: "nist80053", VersionLabel: "r5"},
		{ChunkID: 3, FrameworkCode: "iso27001", VersionLabel: "2022"},
	}
	got := bestHitPerFramework(hits)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].ChunkID != 1 || got[1].ChunkID != 3 {
		t.Errorf("expected chunks [1, 3], got [%d, %d]", got[0].ChunkID, got[1].ChunkID)
	}
}

func TestBuildVersionFilterCTE_CurrentOnly(t *testing.T) {
	res := resolved{currentOnly: true}
	cte, args := buildVersionFilterCTE(res, 1)
	if cte == "" {
		t.Fatal("expected CTE for currentOnly, got empty")
	}
	if len(args) != 0 {
		t.Errorf("expected 0 args, got %d", len(args))
	}
	if !strings.Contains(cte, "fv.is_current = true") {
		t.Errorf("CTE should filter on is_current: %s", cte)
	}
}

func TestBuildVersionFilterCTE_FrameworkPin(t *testing.T) {
	res := resolved{framework: "nist80053", currentOnly: true}
	cte, args := buildVersionFilterCTE(res, 1)
	if cte == "" {
		t.Fatal("expected CTE")
	}
	if len(args) != 1 || args[0] != "nist80053" {
		t.Errorf("expected [nist80053], got %v", args)
	}
}

func TestBuildVersionFilterCTE_VersionPin(t *testing.T) {
	res := resolved{framework: "nist80053", versionLabel: "r5"}
	cte, args := buildVersionFilterCTE(res, 1)
	if cte == "" {
		t.Fatal("expected CTE")
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
	// When versionLabel is set, is_current should NOT appear.
	if strings.Contains(cte, "is_current") {
		t.Error("version pin should not filter on is_current")
	}
}

func TestBuildVersionFilterCTE_NoFilter(t *testing.T) {
	res := resolved{currentOnly: false}
	cte, args := buildVersionFilterCTE(res, 1)
	if cte != "" {
		t.Errorf("expected empty CTE for no filter, got: %s", cte)
	}
	if len(args) != 0 {
		t.Errorf("expected 0 args, got %d", len(args))
	}
}

func findSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
