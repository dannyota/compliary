// Package eval is compliary's retrieval-quality eval harness. It scores the
// retriever against a citation-keyed golden Q&A set with deterministic metrics
// -- recall@k, MRR@k, current-version precision, and abstention correctness --
// so changes to chunking, indexing, or retrieval can be gated before they lock
// in defaults. compliary is evidence-only; there is no answer model to score.
//
// The metric functions here are pure: they take a golden Case and the actual
// []Hit + an abstain flag and return numbers, with no database, so they are
// unit-tested with synthetic cases. cmd/eval wires the live retriever, runs
// each case, and aggregates these per-case scores into a report + CI gate.
//
// # Retriever interface
//
// pkg/eval defines the minimal Retriever interface the harness needs. The
// Task-4 retrieve port (pkg/rag/retrieve) will satisfy it. Until then, the
// harness compiles and tests against fakes.
//
// The interface deliberately mirrors the shape from banhmi's pkg/rag/retrieve
// (same author; ported pattern, no code dependency) adapted for compliary's
// framework-keyed model: Hit carries FrameworkCode + VersionLabel + CitationNorm
// instead of DocNumber + Validity; SearchOpts carries Framework/VersionLabel
// filters instead of jurisdiction/issuer/doc-type.
package eval

import "context"

// SearchMode selects the retrieval arm(s).
type SearchMode string

const (
	SearchModeHybrid SearchMode = "hybrid"
	SearchModeVector SearchMode = "vector"
	SearchModeBM25   SearchMode = "bm25"
)

// ParseSearchMode validates a mode string.
func ParseSearchMode(s string) (SearchMode, error) {
	switch SearchMode(s) {
	case SearchModeHybrid, SearchModeVector, SearchModeBM25:
		return SearchMode(s), nil
	default:
		return "", errUnknownSearchMode
	}
}

// SearchOpts configures a retrieval query.
type SearchOpts struct {
	Mode    SearchMode // bm25, vector, hybrid; empty = hybrid
	TopK    int        // fused hits returned; 0 = config default
	VectorK int        // vector candidates before fusion; 0 = config default
	BM25K   int        // BM25 candidates before fusion; 0 = config default
	RRFK    int        // RRF constant; 0 = config default
	DocCap  int        // max primary-pass hits per framework; 0 = config default
	// LexWeight scales the BM25 arm's RRF contribution relative to the vector
	// arm. 0 = config default (1.0). Values > 1 boost lexical; < 1 attenuate.
	LexWeight float64

	// Framework narrows retrieval to one framework code (e.g. "nist80053").
	// Empty = all frameworks.
	Framework string
	// VersionLabel pins to a specific version (e.g. "r5"). Empty = current
	// versions only (default behavior).
	VersionLabel string

	// CurrentOnly controls version filtering. nil (default) = current versions
	// lead and a small badged pass of non-current versions follows. true =
	// strict current-only filter. false = no filter, all versions.
	CurrentOnly *bool

	// IncludeWithdrawn lifts the status='active' filter so withdrawn controls
	// are retrievable. Defaults to false; when true, both retrieval arms and
	// citation lookup include status='withdrawn' controls.
	IncludeWithdrawn bool
}

// Hit is one fused retrieval result with the metadata needed to evaluate and
// cite the source. The fields are the minimal surface the eval harness and
// downstream MCP need; the Task-4 retrieve port populates them from gold.chunk
// + silver.control + config.framework_version.
type Hit struct {
	ChunkID       int64
	DocumentID    int64
	FrameworkCode string // e.g. "nist80053"
	VersionLabel  string // e.g. "r5"
	Citation      string // human-facing citation, e.g. "AC-2 Account Management"
	CitationNorm  string // normalized citation for matching, e.g. "AC-2"
	ContextPrefix string // deterministic contextual-retrieval header
	Content       string // matched chunk body
	Score         float64
	Similarity    float64 // vector cosine similarity; 0 when BM25-only
	BM25Score     float64 // raw BM25 score; 0 when vector-only
	VectorRank    int     // 1-based rank in vector arm; 0 = absent
	BM25Rank      int     // 1-based rank in BM25 arm; 0 = absent
	IsCurrent     bool    // framework_version.is_current for this hit's version
}

// Evidence is the retrieval product boundary: ranked hits plus explicit gaps
// and abstention signal.
type Evidence struct {
	Hits     []Hit
	Gaps     []Gap
	Abstain  bool
	TopScore float64
}

// Gap is a reason the evidence is incomplete or uncertain.
type Gap struct {
	Kind         string
	Message      string
	BlocksAnswer bool
}

// Retriever runs the selected retrieval path. The Task-4 port
// (pkg/rag/retrieve) will satisfy this interface. Until then, cmd/eval
// compiles against fakes/test doubles.
type Retriever interface {
	Search(ctx context.Context, query string, opts SearchOpts) ([]Hit, error)
	SearchEvidence(ctx context.Context, query string, opts SearchOpts) (Evidence, error)
}
