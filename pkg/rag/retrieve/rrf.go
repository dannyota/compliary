package retrieve

import "sort"

// ranked is one chunk id at a 1-based position in a single arm's result list.
type ranked struct {
	chunkID    int64
	rank       int     // 1-based: best result is rank 1
	similarity float64 // vector arm: cosine similarity (1 - distance); 0 otherwise
	bm25Score  float64 // lexical arm: raw BM25 score (sparse inner product); 0 otherwise
}

// fusedHit is the output of RRF fusion: a chunk id and its summed RRF score.
type fusedHit struct {
	chunkID    int64
	score      float64
	similarity float64
	bm25Score  float64
	vectorRank int
	bm25Rank   int
}

// fuseRRF combines two ranked lists with Reciprocal Rank Fusion:
//
//	score(d) = sum_arm 1 / (rrfK + rank_arm(d))
//
// vectorList is the full-weight arm; bm25List is scaled by lexWeight.
// Ties break by chunk id ascending for determinism.
func fuseRRF(vectorList, bm25List []ranked, rrfK int, lexWeight float64) []fusedHit {
	if rrfK <= 0 {
		rrfK = defaultRRFK
	}
	if lexWeight <= 0 {
		lexWeight = defaultLexWeight
	}
	k := float64(rrfK)

	byID := make(map[int64]*fusedHit)
	get := func(id int64) *fusedHit {
		h, ok := byID[id]
		if !ok {
			h = &fusedHit{chunkID: id}
			byID[id] = h
		}
		return h
	}

	for _, r := range vectorList {
		if r.rank <= 0 {
			continue
		}
		h := get(r.chunkID)
		h.score += 1.0 / (k + float64(r.rank))
		if h.vectorRank == 0 || r.rank < h.vectorRank {
			h.vectorRank = r.rank
		}
		if r.similarity > h.similarity {
			h.similarity = r.similarity
		}
	}
	for _, r := range bm25List {
		if r.rank <= 0 {
			continue
		}
		h := get(r.chunkID)
		h.score += lexWeight / (k + float64(r.rank))
		if h.bm25Rank == 0 || r.rank < h.bm25Rank {
			h.bm25Rank = r.rank
		}
		if r.bm25Score > h.bm25Score {
			h.bm25Score = r.bm25Score
		}
	}

	if len(byID) == 0 {
		return nil
	}
	out := make([]fusedHit, 0, len(byID))
	for _, h := range byID {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].chunkID < out[j].chunkID
	})
	return out
}
