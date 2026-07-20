package mcp

import (
	"context"
	"fmt"
	"testing"

	"danny.vn/compliary/pkg/eval"
)

// --- Fakes ---

type fakeSearcher struct {
	hits    []eval.Hit
	abstain bool
	gaps    []eval.Gap
	err     error
	lastQ   string
	lastO   eval.SearchOpts
}

func (f *fakeSearcher) Search(ctx context.Context, query string, opts eval.SearchOpts) ([]eval.Hit, error) {
	f.lastQ = query
	f.lastO = opts
	return f.hits, f.err
}

func (f *fakeSearcher) SearchEvidence(ctx context.Context, query string, opts eval.SearchOpts) (eval.Evidence, error) {
	f.lastQ = query
	f.lastO = opts
	if f.err != nil {
		return eval.Evidence{}, f.err
	}
	ev := eval.Evidence{
		Hits:    f.hits,
		Abstain: f.abstain,
		Gaps:    f.gaps,
	}
	if len(f.hits) > 0 {
		ev.TopScore = f.hits[0].Score
	}
	return ev, nil
}

type fakeCorpus struct {
	status CorpusStatusOutput
	gaps   QualityGapsOutput
	doc    DocumentOutput
	err    error
}

func (f *fakeCorpus) CorpusStatus(ctx context.Context) (CorpusStatusOutput, error) {
	return f.status, f.err
}

func (f *fakeCorpus) QualityGaps(ctx context.Context, in QualityGapsInput) (QualityGapsOutput, error) {
	return f.gaps, f.err
}

func (f *fakeCorpus) Document(ctx context.Context, in DocumentInput) (DocumentOutput, error) {
	return f.doc, f.err
}

// --- Tests ---

func TestGuide(t *testing.T) {
	c := NewCore(nil, nil, nil)
	g := c.Guide()
	if g.Purpose == "" {
		t.Error("guide purpose should not be empty")
	}
	if len(g.Tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(g.Tools))
	}
	if len(g.RecommendedFlow) == 0 {
		t.Error("recommended flow should not be empty")
	}
	if len(g.EvidenceContract) == 0 {
		t.Error("evidence contract should not be empty")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	c := NewCore(&fakeSearcher{}, nil, nil)
	_, err := c.Search(context.Background(), SearchInput{Query: ""})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSearch_NoSearcher(t *testing.T) {
	c := NewCore(nil, nil, nil)
	_, err := c.Search(context.Background(), SearchInput{Query: "test"})
	if err == nil {
		t.Error("expected error when searcher is nil")
	}
}

func TestSearch_Hits(t *testing.T) {
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{
				ChunkID:       1,
				FrameworkCode: "nist80053",
				VersionLabel:  "r5",
				Citation:      "AC-2 Account Management",
				CitationNorm:  "AC-2",
				Content:       "body",
				Score:         0.07,
				IsCurrent:     true,
			},
		},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{
		Query:     "access control",
		Framework: "nist80053",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(out.Hits))
	}
	if out.Hits[0].FrameworkCode != "nist80053" {
		t.Errorf("expected nist80053, got %q", out.Hits[0].FrameworkCode)
	}
	if out.Hits[0].VersionStatus != "current" {
		t.Errorf("expected current, got %q", out.Hits[0].VersionStatus)
	}
	if out.Hits[0].Cite == "" {
		t.Error("cite should not be empty")
	}
	if out.Abstain {
		t.Error("should not abstain with hits")
	}

	// Verify SearchOpts forwarding.
	if fs.lastO.Framework != "nist80053" {
		t.Errorf("framework not forwarded: %q", fs.lastO.Framework)
	}
}

func TestSearch_RetrieverAbstentionPassThrough(t *testing.T) {
	// The score-floor decision lives in the retriever; the core must carry its
	// abstain flag and low_confidence gap through unchanged.
	fs := &fakeSearcher{
		hits:    []eval.Hit{{ChunkID: 1, Score: 0.04, Content: "text"}},
		abstain: true,
		gaps: []eval.Gap{{
			Kind:         "low_confidence",
			Message:      "below floor",
			BlocksAnswer: true,
		}},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Abstain {
		t.Error("retriever abstain flag must pass through")
	}
	if len(out.Gaps) == 0 {
		t.Fatal("expected at least one gap")
	}
	found := false
	for _, g := range out.Gaps {
		if g.Kind == "low_confidence" {
			found = true
			if !g.BlocksAnswer {
				t.Error("low_confidence gap should block answer")
			}
		}
	}
	if !found {
		t.Error("expected a low_confidence gap")
	}
}

func TestSearch_NoAbstainByDefault(t *testing.T) {
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{ChunkID: 1, Score: 0.08, Content: "text"},
		},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Abstain {
		t.Error("should not abstain when the retriever did not")
	}
}

func TestSearch_NoHitsAbstain(t *testing.T) {
	fs := &fakeSearcher{hits: nil}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "xyz"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Abstain {
		t.Error("should abstain when no hits")
	}
	found := false
	for _, g := range out.Gaps {
		if g.Kind == "no_evidence" {
			found = true
		}
	}
	if !found {
		t.Error("expected a no_evidence gap")
	}
}

func TestSearch_ReducedProjection(t *testing.T) {
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{ChunkID: 1, Content: "secret body", ContextPrefix: "prefix", Score: 0.07},
		},
	}
	c := NewCore(fs, nil, nil, WithProjection(ProjectionReduced))
	out, err := c.Search(context.Background(), SearchInput{Query: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hits[0].Content != "" {
		t.Errorf("reduced projection should strip content, got %q", out.Hits[0].Content)
	}
	if out.Hits[0].ContextPrefix != "" {
		t.Errorf("reduced projection should strip context_prefix, got %q", out.Hits[0].ContextPrefix)
	}
}

func TestCorpusStatus_NilCorpus(t *testing.T) {
	c := NewCore(nil, nil, nil)
	_, err := c.CorpusStatus(context.Background())
	if err == nil {
		t.Error("expected error when corpus is nil")
	}
}

func TestCorpusStatus_Fake(t *testing.T) {
	fc := &fakeCorpus{
		status: CorpusStatusOutput{
			SearchReady: true,
			Totals: CorpusTotals{
				Frameworks: 11,
				Controls:   3402,
				Chunks:     3402,
			},
		},
	}
	c := NewCore(nil, fc, nil)
	out, err := c.CorpusStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !out.SearchReady {
		t.Error("should be search ready")
	}
	if out.Totals.Controls != 3402 {
		t.Errorf("expected 3402 controls, got %d", out.Totals.Controls)
	}
}

func TestQualityGaps_NilCorpus(t *testing.T) {
	c := NewCore(nil, nil, nil)
	_, err := c.QualityGaps(context.Background(), QualityGapsInput{})
	if err == nil {
		t.Error("expected error when corpus is nil")
	}
}

func TestQualityGaps_Categories(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"", 5, false},
		{"all", 5, false},
		{"unresolved_mappings", 1, false},
		{"deferred_docs", 1, false},
		{"manifest", 1, false},
		{"body_quality", 1, false},
		{"eval_floors", 1, false},
		{"bogus", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := gapCategories(tt.input)
			if tt.err {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Errorf("expected %d categories, got %d", tt.want, len(got))
			}
		})
	}
}

func TestDocument_NilCorpus(t *testing.T) {
	c := NewCore(nil, nil, nil)
	_, err := c.Document(context.Background(), DocumentInput{Citation: "AC-2"})
	if err == nil {
		t.Error("expected error when corpus is nil")
	}
}

func TestDocument_EmptyCitation(t *testing.T) {
	fc := &fakeCorpus{}
	c := NewCore(nil, fc, nil)
	_, err := c.Document(context.Background(), DocumentInput{Citation: ""})
	if err == nil {
		t.Error("expected error for empty citation")
	}
}

func TestDocument_NotFound(t *testing.T) {
	fc := &fakeCorpus{
		doc: DocumentOutput{
			Found: false,
			Gaps: []SearchGap{{
				Kind:         "no_evidence",
				Message:      "control not found",
				BlocksAnswer: true,
			}},
		},
	}
	c := NewCore(nil, fc, nil)
	out, err := c.Document(context.Background(), DocumentInput{Citation: "XX-99"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Error("should not be found")
	}
}

func TestDocument_WithMappings(t *testing.T) {
	fc := &fakeCorpus{
		doc: DocumentOutput{
			Found: true,
			Control: &ControlDetail{
				ControlID:     1,
				Citation:      "AC-2",
				Title:         "Account Management",
				Body:          "body text",
				FrameworkCode: "nist80053",
				VersionLabel:  "r5",
			},
			Mappings: []MappingEdge{
				{Direction: "outbound", FrameworkCode: "ciscontrols", CitationNorm: "5.3", Resolved: true},
				{Direction: "outbound", FrameworkCode: "csaccm", CitationNorm: "IAM-01", Resolved: false},
			},
			InboundMappings: []MappingEdge{
				{Direction: "inbound", FrameworkCode: "nistcsf", CitationNorm: "PR.AA-01", Resolved: true},
			},
		},
	}
	c := NewCore(nil, fc, nil)
	out, err := c.Document(context.Background(), DocumentInput{Citation: "AC-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Mappings) != 2 {
		t.Errorf("expected 2 outbound mappings, got %d", len(out.Mappings))
	}
	if len(out.InboundMappings) != 1 {
		t.Errorf("expected 1 inbound mapping, got %d", len(out.InboundMappings))
	}
	// Check resolved/unresolved honest labeling.
	if out.Mappings[1].Resolved {
		t.Error("csaccm mapping should be unresolved")
	}
}

func TestNormalizeLimit(t *testing.T) {
	tests := []struct {
		got, def, max, want int
	}{
		{0, 20, 100, 20},
		{-1, 20, 100, 20},
		{5, 20, 100, 5},
		{50, 20, 100, 50},
		{150, 20, 100, 100},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("got=%d,def=%d,max=%d", tt.got, tt.def, tt.max)
		t.Run(name, func(t *testing.T) {
			if result := normalizeLimit(tt.got, tt.def, tt.max); result != tt.want {
				t.Errorf("normalizeLimit(%d, %d, %d) = %d, want %d",
					tt.got, tt.def, tt.max, result, tt.want)
			}
		})
	}
}

func TestCiteString(t *testing.T) {
	tests := []struct {
		citation, fw, vl, want string
	}{
		{"AC-2", "nist80053", "r5", "AC-2, nist80053 r5"},
		{"A.5.1", "iso27001", "", "A.5.1, iso27001"},
		{"", "nist80053", "r5", "nist80053 r5"},
		{"AC-2", "", "", "AC-2"},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("%s/%s/%s", tt.citation, tt.fw, tt.vl)
		t.Run(name, func(t *testing.T) {
			if got := citeString(tt.citation, tt.fw, tt.vl); got != tt.want {
				t.Errorf("citeString(%q, %q, %q) = %q, want %q",
					tt.citation, tt.fw, tt.vl, got, tt.want)
			}
		})
	}
}

func TestVersionStatus(t *testing.T) {
	if versionStatus(true) != "current" {
		t.Error("expected current")
	}
	if versionStatus(false) != "superseded" {
		t.Error("expected superseded")
	}
}

func TestZeroPadCitation(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"AC-2", "AC-02"},
		{"AC-2(3)", "AC-02(03)"},
		{"AC-02", "AC-02"},
		{"AC-02(03)", "AC-02(03)"},
		{"A.5.1", "A.5.1"},
		{"CC6.1", "CC6.1"},
		{"PR.AA-01", "PR.AA-01"},
		{"AC-12", "AC-12"},
		{"AC-2(13)", "AC-02(13)"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := zeroPadCitation(tt.in); got != tt.want {
				t.Errorf("zeroPadCitation(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDocumentIncludes(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		want    []string
		omitted []string
	}{
		{"default", nil, []string{"chunks", "mappings", "lineage", "children"}, nil},
		{"empty", []string{}, []string{"chunks", "mappings", "lineage", "children"}, nil},
		{"chunks only", []string{"chunks"}, []string{"chunks"}, []string{"mappings", "lineage"}},
		{"unknown ignored", []string{"bogus", "chunks"}, []string{"chunks"}, []string{"bogus"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := documentIncludes(tt.include)
			for _, w := range tt.want {
				if !got[w] {
					t.Errorf("expected %q in includes", w)
				}
			}
			for _, o := range tt.omitted {
				if got[o] {
					t.Errorf("expected %q omitted", o)
				}
			}
		})
	}
}

func TestStaticBodyQualityCaveats(t *testing.T) {
	caveats := staticBodyQualityCaveats()
	if len(caveats) < 3 {
		t.Errorf("expected at least 3 caveats, got %d", len(caveats))
	}
	for _, c := range caveats {
		if c.Framework == "" || c.Description == "" {
			t.Error("caveat fields must not be empty")
		}
	}
}

func TestStaticEvalFloors(t *testing.T) {
	floors := staticEvalFloors()
	if len(floors) < 4 {
		t.Errorf("expected at least 4 floors, got %d", len(floors))
	}
	for _, f := range floors {
		if f.Metric == "" {
			t.Error("floor metric must not be empty")
		}
		if f.Floor <= 0 {
			t.Errorf("floor %q should be positive", f.Metric)
		}
	}
}
