package mcp

import (
	"context"
	"fmt"
	"strings"

	"danny.vn/compliary/pkg/eval"
)

// SearchInput is the search tool's argument schema.
type SearchInput struct {
	Query            string `json:"query"`
	Framework        string `json:"framework,omitempty"`
	VersionLabel     string `json:"version_label,omitempty"`
	IncludeWithdrawn bool   `json:"include_withdrawn,omitempty"`
	TopK             int    `json:"top_k,omitempty"`
	Mode             string `json:"mode,omitempty"`
}

// SearchHit is one retrieved chunk shaped for the search tool.
type SearchHit struct {
	ChunkID       int64   `json:"chunk_id"`
	DocumentID    int64   `json:"document_id"`
	FrameworkCode string  `json:"framework_code"`
	VersionLabel  string  `json:"version_label"`
	Citation      string  `json:"citation"`
	CitationNorm  string  `json:"citation_norm"`
	ContextPrefix string  `json:"context_prefix,omitempty"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	Similarity    float64 `json:"similarity,omitempty"`
	BM25Score     float64 `json:"bm25_score,omitempty"`
	VectorRank    int     `json:"vector_rank,omitempty"`
	BM25Rank      int     `json:"bm25_rank,omitempty"`
	IsCurrent     bool    `json:"is_current"`
	VersionStatus string  `json:"version_status"`
	Cite          string  `json:"cite,omitempty"`
}

// SearchGap is a reason the evidence is incomplete.
type SearchGap struct {
	Kind         string `json:"kind"`
	Message      string `json:"message,omitempty"`
	BlocksAnswer bool   `json:"blocks_answer"`
}

// SearchOutput is the search tool's structured result.
type SearchOutput struct {
	Hits    []SearchHit `json:"hits"`
	Gaps    []SearchGap `json:"gaps,omitempty"`
	Abstain bool        `json:"abstain"`
}

// Search runs hybrid retrieval with score-floor abstention.
func (c *Core) Search(ctx context.Context, in SearchInput) (SearchOutput, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return SearchOutput{}, fmt.Errorf("query is required")
	}
	if c.searcher == nil {
		return SearchOutput{}, fmt.Errorf("searcher is not configured")
	}

	opts := eval.SearchOpts{
		TopK:             in.TopK,
		Framework:        in.Framework,
		VersionLabel:     in.VersionLabel,
		IncludeWithdrawn: in.IncludeWithdrawn,
	}
	if in.Mode != "" {
		mode, err := eval.ParseSearchMode(in.Mode)
		if err != nil {
			return SearchOutput{}, fmt.Errorf("invalid mode %q: %w", in.Mode, err)
		}
		opts.Mode = mode
	}

	ev, err := c.searcher.SearchEvidence(ctx, query, opts)
	if err != nil {
		c.log.Error("mcp: search", "err", err)
		return SearchOutput{}, fmt.Errorf("search: %w", err)
	}

	out := SearchOutput{
		Hits:    make([]SearchHit, 0, len(ev.Hits)),
		Abstain: ev.Abstain,
	}

	for _, h := range ev.Hits {
		sh := SearchHit{
			ChunkID:       h.ChunkID,
			DocumentID:    h.DocumentID,
			FrameworkCode: h.FrameworkCode,
			VersionLabel:  h.VersionLabel,
			Citation:      h.Citation,
			CitationNorm:  h.CitationNorm,
			ContextPrefix: h.ContextPrefix,
			Content:       h.Content,
			Score:         h.Score,
			Similarity:    h.Similarity,
			BM25Score:     h.BM25Score,
			VectorRank:    h.VectorRank,
			BM25Rank:      h.BM25Rank,
			IsCurrent:     h.IsCurrent,
			VersionStatus: versionStatus(h.IsCurrent),
			Cite:          citeString(h.Citation, h.FrameworkCode, h.VersionLabel),
		}
		out.Hits = append(out.Hits, c.projectHit(sh))
	}

	// Score-floor abstention.
	if c.scoreFloor > 0 && len(out.Hits) > 0 && ev.TopScore < c.scoreFloor {
		out.Abstain = true
		out.Gaps = append(out.Gaps, SearchGap{
			Kind:         "low_confidence",
			Message:      fmt.Sprintf("top score %.5f is below the configured floor %.5f; the query may be outside the corpus scope", ev.TopScore, c.scoreFloor),
			BlocksAnswer: true,
		})
	}

	// No-evidence gap.
	if len(out.Hits) == 0 {
		out.Abstain = true
		out.Gaps = append(out.Gaps, SearchGap{
			Kind:         "no_evidence",
			Message:      "no chunks matched the query",
			BlocksAnswer: true,
		})
	}

	// Carry through retriever gaps.
	for _, g := range ev.Gaps {
		out.Gaps = append(out.Gaps, SearchGap{
			Kind:         g.Kind,
			Message:      g.Message,
			BlocksAnswer: g.BlocksAnswer,
		})
	}

	return out, nil
}
