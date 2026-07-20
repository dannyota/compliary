package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/mcp"
)

func TestLandingHandler(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("healthy_shows_stats", func(t *testing.T) {
		status := func(context.Context) (mcp.CorpusStatusOutput, error) {
			return mcp.CorpusStatusOutput{
				Totals: mcp.CorpusTotals{Frameworks: 11, Controls: 3402, Chunks: 3402, Resolved: 2056},
				Frameworks: []mcp.FrameworkVersionStatus{
					{FrameworkCode: "pcidss", FrameworkName: "PCI DSS", VersionLabel: "v4.0.1", IsCurrent: true, Controls: 366, Chunks: 366},
				},
			}, nil
		}
		w := httptest.NewRecorder()
		landingHandler("0.1.1-test", status, log)(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("content-type = %q, want text/html", ct)
		}
		body := w.Body.String()
		for _, want := range []string{"compliary", "operational", "0.1.1-test", "3402", "PCI DSS", "v4.0.1", "/mcp"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
	})

	t.Run("degraded_when_corpus_errors", func(t *testing.T) {
		status := func(context.Context) (mcp.CorpusStatusOutput, error) {
			return mcp.CorpusStatusOutput{}, errors.New("db down")
		}
		w := httptest.NewRecorder()
		landingHandler("0.1.1-test", status, log)(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("degraded should still render 200, got %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "degraded") {
			t.Error("degraded page should say so")
		}
	})
}
