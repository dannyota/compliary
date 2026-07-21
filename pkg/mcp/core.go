// Package mcp is compliary's MCP evidence surface: five tools (guide,
// corpus_status, quality_gaps, search, document) over the retrieval core
// (pkg/rag/retrieve) and the DB stores, shaped for user-owned agents connecting
// over the Model Context Protocol. Evidence-only: no answer LLM, no prose
// synthesis. The connecting model decides the answer from the returned
// structured data.
//
// This package implements the QUERY CORE (Task 1): typed tool logic + DB
// helpers. Task 2 adds the transports (stdio, HTTP) and auth boundary.
//
// Ported from banhmi's pkg/mcp (same author); jurisdiction machinery dropped,
// framework registry dimension and cross-framework mapping traversal added.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"danny.vn/compliary/pkg/eval"
)

// Searcher is the retrieval interface the MCP surface needs. *retrieve.Retriever
// satisfies it.
type Searcher interface {
	Search(ctx context.Context, query string, opts eval.SearchOpts) ([]eval.Hit, error)
	SearchEvidence(ctx context.Context, query string, opts eval.SearchOpts) (eval.Evidence, error)
}

// CorpusReader is the DB-backed corpus slice the MCP surface exposes. dbCorpus
// implements it; tests inject fakes.
type CorpusReader interface {
	CorpusStatus(ctx context.Context) (CorpusStatusOutput, error)
	QualityGaps(ctx context.Context, in QualityGapsInput) (QualityGapsOutput, error)
	Document(ctx context.Context, in DocumentInput) (DocumentOutput, error)
	// FrameworkVersions maps framework code → version labels present in the
	// corpus (silver documents). Used to turn "no results" into explicit
	// unknown_framework / version_not_found gaps.
	FrameworkVersions(ctx context.Context) (map[string][]string, error)
}

// Projection controls whether the response includes verbatim licensed text
// (full) or only citations/paraphrased titles/scores (reduced). Full is for
// authenticated + local-stdio callers; reduced is for unauthenticated HTTP.
type Projection int

const (
	ProjectionFull    Projection = iota // body, title_original, chunk content
	ProjectionReduced                   // citations, paraphrased titles, scores only
)

// Core bundles the five MCP tool implementations over the shared retrieval +
// corpus layer. Task 2 wires it to MCP transports.
type Core struct {
	searcher   Searcher
	corpus     CorpusReader
	log        *slog.Logger
	projection Projection
}

// Option configures the Core.
type Option func(*Core)

// WithProjection sets the projection mode. Default: ProjectionFull.
func WithProjection(p Projection) Option {
	return func(c *Core) { c.projection = p }
}

// NewCore builds the evidence query core.
func NewCore(searcher Searcher, corpus CorpusReader, log *slog.Logger, opts ...Option) *Core {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &Core{
		searcher:   searcher,
		corpus:     corpus,
		log:        log,
		projection: ProjectionFull,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// DBCorpus builds a CorpusReader backed by a live database pool.
func DBCorpus(pool *pgxpool.Pool) CorpusReader {
	return &dbCorpus{pool: pool}
}

type dbCorpus struct {
	pool *pgxpool.Pool
}

// --- helpers ---

func normalizeLimit(got, def, max int) int {
	if got <= 0 {
		return def
	}
	if got > max {
		return max
	}
	return got
}

// citeString builds a ready-to-paste citation: citation + framework/version.
func citeString(citation, frameworkCode, versionLabel string) string {
	var parts []string
	if c := strings.TrimSpace(citation); c != "" {
		parts = append(parts, c)
	}
	fw := strings.TrimSpace(frameworkCode)
	vl := strings.TrimSpace(versionLabel)
	if fw != "" {
		label := fw
		if vl != "" {
			label = fw + " " + vl
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// versionStatus returns a short label for a framework version's currency.
func versionStatus(isCurrent bool) string {
	if isCurrent {
		return "current"
	}
	return "superseded"
}

// projectHit applies the projection to a search hit. Under reduced projection,
// verbatim licensed fields (Content, body) are stripped.
func (c *Core) projectHit(h SearchHit) SearchHit {
	if c.projection == ProjectionReduced {
		h.Content = ""
		h.ContextPrefix = ""
	}
	return h
}

// ProjectDocument applies the projection to a document output. Under reduced,
// body/title_original/chunk content are stripped from controls and chunks.
func (c *Core) ProjectDocument(out DocumentOutput) DocumentOutput {
	if c.projection == ProjectionReduced {
		// Strip licensed fields from the control itself.
		if out.Control != nil {
			out.Control.Body = ""
			out.Control.TitleOriginal = ""
		}
		for i := range out.Chunks {
			out.Chunks[i].Content = ""
			out.Chunks[i].ContextPrefix = ""
		}
		// Amendment bodies carry verbatim instruction text; the citation,
		// action, and neutral title are structural and survive.
		for i := range out.AmendedBy {
			out.AmendedBy[i].Body = ""
		}
		// Mapping edges and version lineage are structural (no licensed
		// text), so they survive reduced projection unchanged.
	}
	return out
}

// -- error helpers --

func errCorpusNotConfigured() error {
	return fmt.Errorf("corpus database is not configured")
}
