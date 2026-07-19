package retrieve

import (
	"math"
	"testing"
)

func TestFuseRRF_Empty(t *testing.T) {
	if got := fuseRRF(nil, nil, 60, 1.0); got != nil {
		t.Fatalf("expected nil, got %d results", len(got))
	}
}

func TestFuseRRF_VectorOnly(t *testing.T) {
	vec := []ranked{
		{chunkID: 10, rank: 1, similarity: 0.9},
		{chunkID: 20, rank: 2, similarity: 0.8},
	}
	got := fuseRRF(vec, nil, 60, 1.0)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// Rank 1: score = 1/(60+1) = 0.01639...; Rank 2: 1/(60+2) = 0.01613...
	if got[0].chunkID != 10 {
		t.Errorf("expected chunk 10 first, got %d", got[0].chunkID)
	}
	if got[0].vectorRank != 1 {
		t.Errorf("expected vectorRank 1, got %d", got[0].vectorRank)
	}
	if got[0].similarity != 0.9 {
		t.Errorf("expected similarity 0.9, got %f", got[0].similarity)
	}
}

func TestFuseRRF_BM25Only(t *testing.T) {
	bm25 := []ranked{
		{chunkID: 30, rank: 1, bm25Score: 5.2},
		{chunkID: 40, rank: 2, bm25Score: 3.1},
	}
	got := fuseRRF(nil, bm25, 60, 1.0)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].chunkID != 30 {
		t.Errorf("expected chunk 30 first, got %d", got[0].chunkID)
	}
	if got[0].bm25Rank != 1 {
		t.Errorf("expected bm25Rank 1, got %d", got[0].bm25Rank)
	}
	if got[0].bm25Score != 5.2 {
		t.Errorf("expected bm25Score 5.2, got %f", got[0].bm25Score)
	}
}

func TestFuseRRF_Hybrid(t *testing.T) {
	vec := []ranked{
		{chunkID: 1, rank: 1, similarity: 0.95},
		{chunkID: 2, rank: 2, similarity: 0.90},
		{chunkID: 3, rank: 3, similarity: 0.85},
	}
	bm25 := []ranked{
		{chunkID: 2, rank: 1, bm25Score: 7.0},
		{chunkID: 4, rank: 2, bm25Score: 5.0},
		{chunkID: 1, rank: 3, bm25Score: 3.0},
	}
	got := fuseRRF(vec, bm25, 60, 1.0)
	if len(got) != 4 {
		t.Fatalf("expected 4, got %d", len(got))
	}

	// Chunk 2 appears in both arms (vec rank 2, bm25 rank 1) => highest score.
	// score = 1/(60+2) + 1/(60+1) = 0.01613 + 0.01639 = 0.03252
	if got[0].chunkID != 2 {
		t.Errorf("expected chunk 2 first (both arms), got %d", got[0].chunkID)
	}
	expectedScore := 1.0/62 + 1.0/61
	if math.Abs(got[0].score-expectedScore) > 1e-10 {
		t.Errorf("chunk 2 score: want %f, got %f", expectedScore, got[0].score)
	}
	if got[0].vectorRank != 2 || got[0].bm25Rank != 1 {
		t.Errorf("chunk 2 ranks: want vec=2 bm25=1, got vec=%d bm25=%d",
			got[0].vectorRank, got[0].bm25Rank)
	}

	// Chunk 1: vec rank 1, bm25 rank 3.
	// score = 1/(60+1) + 1/(60+3) = 0.01639 + 0.01587 = 0.03226
	if got[1].chunkID != 1 {
		t.Errorf("expected chunk 1 second, got %d", got[1].chunkID)
	}
}

func TestFuseRRF_LexWeight(t *testing.T) {
	vec := []ranked{{chunkID: 1, rank: 1, similarity: 0.9}}
	bm25 := []ranked{{chunkID: 2, rank: 1, bm25Score: 5.0}}

	// With lexWeight=0.5, BM25 arm contributes half.
	got := fuseRRF(vec, bm25, 60, 0.5)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	vecScore := 1.0 / 61
	bm25Score := 0.5 / 61
	if got[0].chunkID != 1 {
		t.Errorf("vector-only chunk should lead with higher score")
	}
	if math.Abs(got[0].score-vecScore) > 1e-10 {
		t.Errorf("chunk 1 score: want %f, got %f", vecScore, got[0].score)
	}
	if math.Abs(got[1].score-bm25Score) > 1e-10 {
		t.Errorf("chunk 2 score: want %f, got %f", bm25Score, got[1].score)
	}
}

func TestFuseRRF_TieBreakByChunkID(t *testing.T) {
	// Two chunks at the same rank in different arms -> same score; lower ID wins.
	vec := []ranked{{chunkID: 100, rank: 1, similarity: 0.9}}
	bm25 := []ranked{{chunkID: 50, rank: 1, bm25Score: 5.0}}
	got := fuseRRF(vec, bm25, 60, 1.0)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	// Both have score 1/61; chunk 50 < chunk 100.
	if got[0].chunkID != 50 {
		t.Errorf("tie-break: expected chunk 50 first, got %d", got[0].chunkID)
	}
}

func TestFuseRRF_DefaultRRFK(t *testing.T) {
	vec := []ranked{{chunkID: 1, rank: 1}}
	// rrfK <= 0 should use defaultRRFK.
	got := fuseRRF(vec, nil, 0, 1.0)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	expected := 1.0 / float64(defaultRRFK+1)
	if math.Abs(got[0].score-expected) > 1e-10 {
		t.Errorf("expected score %f (default rrfK=%d), got %f", expected, defaultRRFK, got[0].score)
	}
}

func TestFuseRRF_SkipsZeroRank(t *testing.T) {
	vec := []ranked{{chunkID: 1, rank: 0}} // invalid rank
	got := fuseRRF(vec, nil, 60, 1.0)
	if got != nil {
		t.Fatalf("expected nil for zero-rank input, got %d results", len(got))
	}
}
