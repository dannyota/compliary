package mcp

import (
	"context"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/eval"
)

func TestSearch_DetailCompact(t *testing.T) {
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{
				ChunkID:       1,
				FrameworkCode: "nist80053",
				VersionLabel:  "r5",
				Citation:      "AC-2 Account Management",
				CitationNorm:  "AC-2",
				ContextPrefix: "NIST SP 800-53 r5 > Access Control > AC-2",
				Content:       "body text that should be stripped",
				Score:         0.07,
				IsCurrent:     true,
			},
		},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "access", Detail: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(out.Hits))
	}
	h := out.Hits[0]
	if h.Content != "" {
		t.Errorf("compact detail should strip Content, got %q", h.Content)
	}
	if h.ContextPrefix != "" {
		t.Errorf("compact detail should strip ContextPrefix, got %q", h.ContextPrefix)
	}
	// Structural fields must survive.
	if h.Citation == "" {
		t.Error("compact detail should keep Citation")
	}
	if h.CitationNorm == "" {
		t.Error("compact detail should keep CitationNorm")
	}
	if h.FrameworkCode == "" {
		t.Error("compact detail should keep FrameworkCode")
	}
	if h.VersionLabel == "" {
		t.Error("compact detail should keep VersionLabel")
	}
	if h.Score == 0 {
		t.Error("compact detail should keep Score")
	}
	if h.VersionStatus == "" {
		t.Error("compact detail should keep VersionStatus")
	}
	if h.Cite == "" {
		t.Error("compact detail should keep Cite")
	}
}

func TestSearch_DetailStandard(t *testing.T) {
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{
				ChunkID:       1,
				FrameworkCode: "nist80053",
				VersionLabel:  "r5",
				Citation:      "AC-2 Account Management",
				CitationNorm:  "AC-2",
				ContextPrefix: "prefix",
				Content:       "body text",
				Score:         0.07,
				IsCurrent:     true,
			},
		},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "access", Detail: "standard"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hits[0].Content == "" {
		t.Error("standard detail should keep Content")
	}
	if out.Hits[0].ContextPrefix == "" {
		t.Error("standard detail should keep ContextPrefix")
	}
}

func TestSearch_DetailDefault(t *testing.T) {
	// Empty detail should behave as standard (keep content).
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{ChunkID: 1, Content: "body", ContextPrefix: "prefix", Score: 0.07},
		},
	}
	c := NewCore(fs, nil, nil)
	out, err := c.Search(context.Background(), SearchInput{Query: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hits[0].Content == "" {
		t.Error("default (empty) detail should keep Content")
	}
}

func TestSearch_DetailInvalid(t *testing.T) {
	fs := &fakeSearcher{}
	c := NewCore(fs, nil, nil)
	_, err := c.Search(context.Background(), SearchInput{Query: "test", Detail: "verbose"})
	if err == nil {
		t.Fatal("expected error for unknown detail level")
	}
	if !strings.Contains(err.Error(), "compact") || !strings.Contains(err.Error(), "standard") {
		t.Errorf("error should list valid detail levels, got %q", err.Error())
	}
}

func TestSearch_DetailCompactWithReducedProjection(t *testing.T) {
	// Compact + reduced projection: both strip content; compact is orthogonal.
	fs := &fakeSearcher{
		hits: []eval.Hit{
			{
				ChunkID:       1,
				Content:       "secret body",
				ContextPrefix: "secret prefix",
				Score:         0.07,
				FrameworkCode: "nist80053",
				VersionLabel:  "r5",
				Citation:      "AC-2",
				CitationNorm:  "AC-2",
				IsCurrent:     true,
			},
		},
	}
	c := NewCore(fs, nil, nil, WithProjection(ProjectionReduced))
	out, err := c.Search(context.Background(), SearchInput{Query: "test", Detail: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	h := out.Hits[0]
	if h.Content != "" {
		t.Errorf("compact+reduced should strip Content, got %q", h.Content)
	}
	if h.ContextPrefix != "" {
		t.Errorf("compact+reduced should strip ContextPrefix, got %q", h.ContextPrefix)
	}
	// Structural fields survive both.
	if h.Citation == "" {
		t.Error("compact+reduced should keep Citation")
	}
	if h.Score == 0 {
		t.Error("compact+reduced should keep Score")
	}
	if h.VersionStatus == "" {
		t.Error("compact+reduced should keep VersionStatus")
	}
}

func TestSearch_FilterGaps(t *testing.T) {
	fs := &fakeSearcher{hits: nil} // empty result triggers filter diagnostics
	fc := &fakeCorpus{frameworkVersions: map[string][]string{
		"iso27001": {"2022"},
		"pcidss":   {"v4.0.1"},
	}}
	c := NewCore(fs, fc, nil)

	t.Run("unknown_framework", func(t *testing.T) {
		out, err := c.Search(context.Background(), SearchInput{Query: "x", Framework: "iso9001"})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, g := range out.Gaps {
			if g.Kind == "unknown_framework" {
				found = true
				if !strings.Contains(g.Message, "iso27001") || !strings.Contains(g.Message, "pcidss") {
					t.Errorf("gap should list known codes: %q", g.Message)
				}
			}
		}
		if !found {
			t.Errorf("expected unknown_framework gap, got %+v", out.Gaps)
		}
	})

	t.Run("version_not_found", func(t *testing.T) {
		out, err := c.Search(context.Background(), SearchInput{Query: "x", Framework: "iso27001", VersionLabel: "2013"})
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, g := range out.Gaps {
			if g.Kind == "version_not_found" {
				found = true
				if !strings.Contains(g.Message, "2022") {
					t.Errorf("gap should list available versions: %q", g.Message)
				}
			}
		}
		if !found {
			t.Errorf("expected version_not_found gap, got %+v", out.Gaps)
		}
	})

	t.Run("valid_filter_no_extra_gaps", func(t *testing.T) {
		out, err := c.Search(context.Background(), SearchInput{Query: "x", Framework: "iso27001", VersionLabel: "2022"})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range out.Gaps {
			if g.Kind == "unknown_framework" || g.Kind == "version_not_found" {
				t.Errorf("valid filter should not gap: %+v", g)
			}
		}
	})
}
