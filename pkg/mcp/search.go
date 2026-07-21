package mcp

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"danny.vn/compliary/pkg/eval"
)

// SearchInput is the search tool's argument schema.
type SearchInput struct {
	Query            string `json:"query" jsonschema:"compliance question or control citation, in English (e.g. 'multi-factor authentication for remote access' or 'AC-2(3)')"`
	Framework        string `json:"framework,omitempty" jsonschema:"framework code filter — raises recall to ~83% (vs ~67% unfiltered); codes are listed by corpus_status (e.g. nist80053, iso27001, pcidss, ciscontrols, soc2tsc)"`
	VersionLabel     string `json:"version_label,omitempty" jsonschema:"pin one framework version (e.g. r5, 2022, v4.0.1); omit to search the current version"`
	IncludeWithdrawn bool   `json:"include_withdrawn,omitempty" jsonschema:"also retrieve withdrawn controls (e.g. 800-53r5's incorporated-into families); default false"`
	TopK             int    `json:"top_k,omitempty" jsonschema:"number of hits to return; default 8"`
	Mode             string `json:"mode,omitempty" jsonschema:"retrieval arm: hybrid (default), vector, or bm25"`
	Detail           string `json:"detail,omitempty" jsonschema:"response detail level: standard (default) returns full hit shape; compact strips content and context_prefix for cheap discovery — read full text via document include=[chunks]"`
}

// validSearchDetails lists the accepted values for SearchInput.Detail.
var validSearchDetails = map[string]bool{
	"standard": true,
	"compact":  true,
}

// validateDetail rejects unknown detail values, matching the include/category
// error style: hard error naming the valid set.
func validateDetail(detail string) error {
	if detail == "" {
		return nil
	}
	if !validSearchDetails[detail] {
		return fmt.Errorf("unknown detail level %q; valid: compact, standard", detail)
	}
	return nil
}

// SearchHit is one retrieved chunk shaped for the search tool. Retrieval
// internals (chunk/document IDs, per-arm scores and ranks) stay server-side —
// an agent can act only on the citation, content, score, and version badge.
type SearchHit struct {
	FrameworkCode string  `json:"framework_code"`
	VersionLabel  string  `json:"version_label"`
	Citation      string  `json:"citation"`
	CitationNorm  string  `json:"citation_norm"`
	ContextPrefix string  `json:"context_prefix,omitempty"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	IsCurrent     bool    `json:"is_current"`
	VersionStatus string  `json:"version_status"`
	Cite          string  `json:"cite,omitempty"`
	SourceURL     string  `json:"source_url,omitempty"`
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
	if err := validateDetail(in.Detail); err != nil {
		return SearchOutput{}, err
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

	sourceURLs := c.hitSourceURLs(ctx, ev.Hits)
	for _, h := range ev.Hits {
		sh := SearchHit{
			FrameworkCode: h.FrameworkCode,
			VersionLabel:  h.VersionLabel,
			Citation:      h.Citation,
			CitationNorm:  h.CitationNorm,
			ContextPrefix: h.ContextPrefix,
			Content:       h.Content,
			Score:         h.Score,
			IsCurrent:     h.IsCurrent,
			VersionStatus: versionStatus(h.IsCurrent),
			Cite:          citeString(h.Citation, h.FrameworkCode, h.VersionLabel),
			SourceURL:     sourceURLs[h.DocumentID],
		}
		hit := c.projectHit(sh)
		if in.Detail == "compact" {
			hit.Content = ""
			hit.ContextPrefix = ""
		}
		out.Hits = append(out.Hits, hit)
	}

	// Score-floor abstention lives in the retriever (raw-cosine comparison,
	// measurable by cmd/eval); its low_confidence gap arrives via ev.Gaps below.

	// No-evidence gap.
	if len(out.Hits) == 0 {
		out.Abstain = true
		out.Gaps = append(out.Gaps, SearchGap{
			Kind:         "no_evidence",
			Message:      "no chunks matched the query",
			BlocksAnswer: true,
		})
	}

	// Distinguish "nothing matched" from "the filter itself is wrong": an
	// unknown framework code or a version not in the corpus must always be
	// named. With zero hits the gap blocks; with hits it is advisory — the
	// retriever filtered in SQL, so hits mean the filter matched, but a
	// future soft-filter fallback must not silently drop the diagnostic.
	filterGaps := c.filterGaps(ctx, in.Framework, in.VersionLabel)
	if len(out.Hits) > 0 {
		for i := range filterGaps {
			filterGaps[i].BlocksAnswer = false
		}
	}
	out.Gaps = append(out.Gaps, filterGaps...)

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

// hitSourceURLs batch-resolves the official publisher page for each hit's
// document, so every hit can carry a verifiable source link. Best-effort:
// a nil corpus (tests, degraded mode) or a lookup failure yields no URLs,
// never an error — the hits themselves are the evidence.
func (c *Core) hitSourceURLs(ctx context.Context, hits []eval.Hit) map[int64]string {
	if c.corpus == nil || len(hits) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(hits))
	seen := make(map[int64]bool, len(hits))
	for _, h := range hits {
		if h.DocumentID != 0 && !seen[h.DocumentID] {
			seen[h.DocumentID] = true
			ids = append(ids, h.DocumentID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	urls, err := c.corpus.DocumentSourceURLs(ctx, ids)
	if err != nil {
		c.log.Warn("mcp: source url lookup failed", "err", err)
		return nil
	}
	return urls
}

// filterGaps checks a search's framework/version filter against the corpus
// and returns explicit gaps when the filter names something that is not there.
// Nil corpus (tests, degraded mode) → no gaps.
func (c *Core) filterGaps(ctx context.Context, framework, versionLabel string) []SearchGap {
	if framework == "" || c.corpus == nil {
		return nil
	}
	fv, err := c.corpus.FrameworkVersions(ctx)
	if err != nil {
		c.log.Warn("mcp: framework versions lookup failed", "err", err)
		return nil
	}
	versions, ok := fv[framework]
	if !ok {
		known := make([]string, 0, len(fv))
		for k := range fv {
			known = append(known, k)
		}
		sort.Strings(known)
		return []SearchGap{{
			Kind:         "unknown_framework",
			Message:      fmt.Sprintf("framework %q is not in the corpus; known codes: %s", framework, strings.Join(known, ", ")),
			BlocksAnswer: true,
		}}
	}
	if versionLabel != "" && !slices.Contains(versions, versionLabel) {
		return []SearchGap{{
			Kind:         "version_not_found",
			Message:      fmt.Sprintf("framework %q has no version %q in the corpus; available: %s", framework, versionLabel, strings.Join(versions, ", ")),
			BlocksAnswer: true,
		}}
	}
	return nil
}
