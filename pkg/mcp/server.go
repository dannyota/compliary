// server.go wires the Core's five tools into the official Go MCP SDK
// (github.com/modelcontextprotocol/go-sdk). The transport (stdio or HTTP) is
// the caller's responsibility (cmd/mcp, cmd/server); this package provides
// Run (stdio) and HTTPHandler (streamable HTTP). Ported from banhmi's
// pkg/mcp/mcp.go; jurisdiction machinery dropped.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the MCP SDK server with compliary's evidence tools registered
// over the query core. Build with NewServer, serve with Run or HTTPHandler.
type Server struct {
	mcp         *mcpsdk.Server
	core        *Core
	log         *slog.Logger
	version     string
	behindProxy bool
}

// ServerOption configures the MCP server.
type ServerOption func(*Server)

// WithVersion stamps the build version into the MCP server identity.
func WithVersion(v string) ServerOption {
	return func(s *Server) { s.version = v }
}

// WithBehindProxy disables the SDK's localhost DNS-rebinding protection so the
// MCP handler works behind reverse proxies where the listener is loopback but
// the Host header is the proxy's public hostname.
func WithBehindProxy() ServerOption {
	return func(s *Server) { s.behindProxy = true }
}

const defaultServerVersion = "0.1.13"

func (s *Server) effectiveVersion() string {
	if s.version != "" {
		return s.version
	}
	return defaultServerVersion
}

// closedWorld and notDestructive are hint values for tool annotations.
var (
	closedWorld    = false
	notDestructive = false
)

// NewServer builds the MCP surface over a Core. log must not write to stdout
// (which is the stdio transport).
func NewServer(core *Core, log *slog.Logger, opts ...ServerOption) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{core: core, log: log}
	for _, opt := range opts {
		opt(s)
	}

	instructions := buildInstructions(core)

	srv := mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:    "compliary",
			Title:   "compliary — InfoSec & Cybersecurity control framework evidence",
			Version: s.effectiveVersion(),
		},
		&mcpsdk.ServerOptions{Logger: log, Instructions: instructions},
	)
	s.mcp = srv

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld, DestructiveHint: &notDestructive, Title: "Guide: how to use compliary"},
		Name:        "guide",
		Description: "Playbook for using compliary's evidence tools: scope, citation forms per framework, version-pin semantics, mapping-edge semantics, gaps philosophy, query tips including the framework filter's recall advantage (~83% vs ~67%).",
	}, s.handleGuide)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld, DestructiveHint: &notDestructive, Title: "Corpus status & coverage"},
		Name:        "corpus_status",
		Description: "Live per-framework/version counts: documents, controls by kind, withdrawn, chunks, embeddings, mapping edges (resolved/unresolved), completeness, last-stage info.",
	}, s.handleCorpusStatus)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld, DestructiveHint: &notDestructive, Title: "Corpus quality gaps"},
		Name:        "quality_gaps",
		Description: "Unresolved mapping edges by target, deferred docs (amendments, CAIQ), unrecognized manifest rows, body-quality caveats (PCI guidance interleave), abstain/eval floors.",
		InputSchema: inputSchemaFor[QualityGapsInput](),
	}, s.handleQualityGaps)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld, DestructiveHint: &notDestructive, Title: "Open a control document"},
		Name:        "document",
		Description: "Citation lookup: control body (verbatim past auth), mapping edges (both directions, resolved + unresolved with honest labels), version lineage, parent/children context. Default version = current; explicit version pin supported.",
		InputSchema: inputSchemaFor[DocumentInput](),
	}, s.handleDocument)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld, DestructiveHint: &notDestructive, Title: "Search control evidence"},
		Name:        "search",
		Description: "Hybrid retrieval (dense + BM25, RRF-fused) over InfoSec control frameworks. Accepts framework and version_label filters, include_withdrawn flag, top_k, mode, detail. detail='compact' is the cheap discovery pass (strips content/context_prefix); detail='standard' (default) returns the full hit shape. Citation-shaped queries are pinned at score 1.0. Score-floor abstention returns an explicit gap notice.",
		InputSchema: inputSchemaFor[SearchInput](),
	}, s.handleSearch)

	return s
}

// Run serves over the given transport until ctx is cancelled. Pass
// &mcpsdk.StdioTransport{} for stdio.
func (s *Server) Run(ctx context.Context, t mcpsdk.Transport) error {
	return s.mcp.Run(ctx, t)
}

// HTTPHandler returns a Streamable HTTP handler for mounting at /mcp. Stateless:
// all tools are read-only queries with no session state. Per the MCP spec a
// stateless server answers the GET/SSE stream with 405.
func (s *Server) HTTPHandler() http.Handler {
	opts := &mcpsdk.StreamableHTTPOptions{
		Stateless:                  true,
		DisableLocalhostProtection: s.behindProxy,
	}
	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return s.mcp }, opts)
}

// --- tool handlers -----------------------------------------------------------

func (s *Server) handleGuide(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, GuideOutput, error) {
	return nil, s.core.Guide(), nil
}

func (s *Server) handleCorpusStatus(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, CorpusStatusOutput, error) {
	out, err := s.core.CorpusStatus(ctx)
	if err != nil {
		return nil, CorpusStatusOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) handleQualityGaps(ctx context.Context, _ *mcpsdk.CallToolRequest, in QualityGapsInput) (*mcpsdk.CallToolResult, QualityGapsOutput, error) {
	out, err := s.core.QualityGaps(ctx, in)
	if err != nil {
		return nil, QualityGapsOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) handleDocument(ctx context.Context, _ *mcpsdk.CallToolRequest, in DocumentInput) (*mcpsdk.CallToolResult, DocumentOutput, error) {
	out, err := s.core.Document(ctx, in)
	if err != nil {
		return nil, DocumentOutput{}, err
	}
	return nil, out, nil
}

func (s *Server) handleSearch(ctx context.Context, _ *mcpsdk.CallToolRequest, in SearchInput) (*mcpsdk.CallToolResult, SearchOutput, error) {
	out, err := s.core.Search(ctx, in)
	if err != nil {
		return nil, SearchOutput{}, err
	}
	return nil, out, nil
}

// --- instructions ------------------------------------------------------------

// buildInstructions returns the server-level instructions describing compliary.
func buildInstructions(core *Core) string {
	base := "compliary is an evidence-only knowledge base for InfoSec & Cybersecurity control frameworks " +
		"(ISO 27001, SOC 2 TSC, PCI DSS, NIST CSF, NIST SP 800-53, CIS Controls, ISO 27002/27017/27018, " +
		"CSA CCM, COBIT). It returns exact control citations, version lineage, cross-framework mapping edges, " +
		"provenance, and explicit gaps. It never answers — you retrieve evidence and decide the answer. " +
		"Query in English (the frameworks' publication language). " +
		"Use the framework filter for higher recall (~83% vs ~67% unfiltered)."

	if core.corpus != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		status, err := core.corpus.CorpusStatus(ctx)
		if err == nil && status.Totals.Controls > 0 {
			base += fmt.Sprintf(" Corpus: %d frameworks, %d controls, %d chunks, %d mapping edges (%d resolved, %d unresolved).",
				status.Totals.Frameworks, status.Totals.Controls, status.Totals.Chunks,
				status.Totals.MappingEdges, status.Totals.Resolved, status.Totals.Unresolved)
			// Stamp the real framework codes so the agent's system prompt
			// carries the filter vocabulary without a corpus_status round trip.
			if codes := frameworkCodes(status.Frameworks); len(codes) > 0 {
				base += " Framework codes: " + strings.Join(codes, ", ") + "."
			}
		} else {
			base += " (Corpus stats unavailable at startup — call corpus_status for live counts.)"
		}
	}
	return base
}

// frameworkCodes returns the distinct framework codes in stable order.
func frameworkCodes(frameworks []FrameworkVersionStatus) []string {
	seen := make(map[string]bool, len(frameworks))
	var codes []string
	for _, f := range frameworks {
		if !seen[f.FrameworkCode] {
			seen[f.FrameworkCode] = true
			codes = append(codes, f.FrameworkCode)
		}
	}
	sort.Strings(codes)
	return codes
}

// --- schema helpers ----------------------------------------------------------

// inputSchemaFor infers the JSON Schema for T exactly as mcp.AddTool would,
// then collapses each optional field's ["null", X] type union to the bare X.
func inputSchemaFor[T any]() any {
	schema, err := jsonschema.ForType(reflect.TypeFor[T](), &jsonschema.ForOptions{})
	if err != nil {
		return nil
	}
	for _, prop := range schema.Properties {
		if len(prop.Types) != 2 {
			continue
		}
		switch {
		case prop.Types[0] == "null":
			prop.Type = prop.Types[1]
		case prop.Types[1] == "null":
			prop.Type = prop.Types[0]
		default:
			continue
		}
		prop.Types = nil
	}
	return schema
}
