package mcp

import (
	"testing"
)

// TestProjectHitFull verifies that full projection keeps all fields.
func TestProjectHitFull(t *testing.T) {
	c := NewCore(nil, nil, nil) // full projection by default
	h := SearchHit{
		FrameworkCode: "nist80053",
		Citation:      "AC-2 Account Management",
		CitationNorm:  "AC-2",
		Content:       "This is verbatim body text from the control.",
		ContextPrefix: "NIST SP 800-53 r5 > Access Control > AC-2",
		Score:         0.07,
	}
	got := c.projectHit(h)
	if got.Content == "" {
		t.Error("full projection should keep Content")
	}
	if got.ContextPrefix == "" {
		t.Error("full projection should keep ContextPrefix")
	}
}

// TestProjectHitReduced verifies that reduced projection strips body/content.
func TestProjectHitReduced(t *testing.T) {
	c := NewCore(nil, nil, nil, WithProjection(ProjectionReduced))
	h := SearchHit{
		FrameworkCode: "nist80053",
		Citation:      "AC-2 Account Management",
		CitationNorm:  "AC-2",
		Content:       "This is verbatim body text from the control.",
		ContextPrefix: "NIST SP 800-53 r5 > Access Control > AC-2",
		Score:         0.07,
		Cite:          "AC-2, nist80053 r5",
	}
	got := c.projectHit(h)
	if got.Content != "" {
		t.Errorf("reduced projection should strip Content, got %q", got.Content)
	}
	if got.ContextPrefix != "" {
		t.Errorf("reduced projection should strip ContextPrefix, got %q", got.ContextPrefix)
	}
	// Structural fields must survive.
	if got.Citation != h.Citation {
		t.Errorf("reduced projection should keep Citation, got %q", got.Citation)
	}
	if got.CitationNorm != h.CitationNorm {
		t.Errorf("reduced projection should keep CitationNorm, got %q", got.CitationNorm)
	}
	if got.Score != h.Score {
		t.Errorf("reduced projection should keep Score, got %f", got.Score)
	}
	if got.Cite != h.Cite {
		t.Errorf("reduced projection should keep Cite, got %q", got.Cite)
	}
	if got.FrameworkCode != h.FrameworkCode {
		t.Errorf("reduced projection should keep FrameworkCode, got %q", got.FrameworkCode)
	}
}

// TestProjectDocumentReduced verifies that reduced projection strips chunk
// content and context_prefix from document output.
func TestProjectDocumentReduced(t *testing.T) {
	c := NewCore(nil, nil, nil, WithProjection(ProjectionReduced))
	doc := DocumentOutput{
		Found: true,
		Control: &ControlDetail{
			ControlID:     1,
			Citation:      "AC-2",
			Title:         "Account Management",
			TitleOriginal: "AC-2 ACCOUNT MANAGEMENT",
			Body:          "The organization manages information system accounts.",
		},
		Chunks: []DocumentChunk{
			{
				ChunkID:       10,
				Citation:      "AC-2",
				ContextPrefix: "NIST SP 800-53 r5 > Access Control > AC-2",
				Content:       "Verbatim text here.",
				Ordinal:       0,
			},
		},
		Mappings: []MappingEdge{
			{
				Direction:     "outbound",
				FrameworkCode: "ciscontrols",
				CitationNorm:  "5.3",
				Resolved:      true,
				Relationship:  "related",
			},
		},
		VersionLineage: []VersionLineageRow{
			{Kind: "version", FrameworkCode: "nist80053", VersionLabel: "r5", IsCurrent: true},
		},
		AmendedBy: []AmendmentRef{
			{
				Citation:  "4.1",
				Action:    "add",
				Qualifier: "amd1-2024",
				DocKey:    "iso27001|2022|amendment:amd1-2024",
				Title:     "Amendment change to clause 4.1",
				Body:      "Add the following sentence at the end of the subclause: …",
			},
		},
	}

	got := c.ProjectDocument(doc)

	// Control Body and TitleOriginal must be stripped (primary licensed fields).
	if got.Control.Body != "" {
		t.Errorf("reduced projection should strip Control.Body, got %q", got.Control.Body)
	}
	if got.Control.TitleOriginal != "" {
		t.Errorf("reduced projection should strip Control.TitleOriginal, got %q", got.Control.TitleOriginal)
	}
	// Paraphrased title must survive.
	if got.Control.Title != "Account Management" {
		t.Errorf("reduced projection should keep Control.Title, got %q", got.Control.Title)
	}

	// Amendment bodies must be stripped; structural fields survive.
	for i, a := range got.AmendedBy {
		if a.Body != "" {
			t.Errorf("amended_by[%d].Body should be stripped, got %q", i, a.Body)
		}
		if a.Citation == "" || a.Action == "" || a.Title == "" {
			t.Errorf("amended_by[%d] structural fields should survive: %+v", i, a)
		}
	}

	// Chunk content must be stripped.
	for i, ch := range got.Chunks {
		if ch.Content != "" {
			t.Errorf("chunk[%d].Content should be stripped, got %q", i, ch.Content)
		}
		if ch.ContextPrefix != "" {
			t.Errorf("chunk[%d].ContextPrefix should be stripped, got %q", i, ch.ContextPrefix)
		}
	}

	// Mapping edges must survive (they are structural, not licensed text).
	if len(got.Mappings) != 1 {
		t.Fatalf("mappings should survive reduced projection, got %d", len(got.Mappings))
	}
	if got.Mappings[0].FrameworkCode != "ciscontrols" {
		t.Errorf("mapping framework should survive, got %q", got.Mappings[0].FrameworkCode)
	}

	// Version lineage must survive.
	if len(got.VersionLineage) != 1 {
		t.Fatalf("lineage should survive reduced projection, got %d", len(got.VersionLineage))
	}
}

// TestProjectHitReduced_TitleOriginalAbsent adversarially checks that
// title_original does not leak through search hits (it never appears in
// SearchHit by design; this test documents that invariant).
func TestProjectHitReduced_TitleOriginalAbsent(t *testing.T) {
	// SearchHit deliberately has no TitleOriginal field. This test exists to
	// document that the reduced surface carries no path for title_original
	// to leak. If someone adds it, this test should be updated to verify
	// stripping.
	h := SearchHit{
		Citation:      "AC-2",
		Content:       "body text",
		ContextPrefix: "prefix",
	}
	c := NewCore(nil, nil, nil, WithProjection(ProjectionReduced))
	got := c.projectHit(h)
	// Structural: only Citation and score fields survive.
	if got.Content != "" {
		t.Error("content must be stripped")
	}
}

// TestProjectDocumentReduced_NestedMappingNoBody verifies that nested mapping
// structures do not carry body text (resolvedTitle is a paraphrased title,
// never title_original).
func TestProjectDocumentReduced_NestedMappingNoBody(t *testing.T) {
	c := NewCore(nil, nil, nil, WithProjection(ProjectionReduced))
	doc := DocumentOutput{
		Found: true,
		Mappings: []MappingEdge{
			{
				Direction:     "outbound",
				FrameworkCode: "iso27001",
				CitationNorm:  "A.5.1",
				Resolved:      true,
				ResolvedTitle: "Information security policies",
				Relationship:  "related",
			},
		},
	}
	got := c.ProjectDocument(doc)
	// ResolvedTitle is the paraphrased title (silver.control.title, not
	// title_original), so it is safe under reduced projection.
	if got.Mappings[0].ResolvedTitle != "Information security policies" {
		t.Errorf("resolvedTitle should survive, got %q", got.Mappings[0].ResolvedTitle)
	}
}
