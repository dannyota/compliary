package mcp

import (
	"context"
	"strings"
	"testing"
)

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
