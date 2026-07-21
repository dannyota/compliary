package main

import (
	"context"
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"danny.vn/compliary/pkg/mcp"
)

//go:embed landing.html
var landingHTML string

var landingTmpl = template.Must(template.New("landing").Funcs(template.FuncMap{
	// pct renders a 0..1 ratio as a percentage ("0.722" → "72.2%").
	"pct": func(v float64) string {
		return strconv.FormatFloat(v*100, 'f', -1, 64) + "%"
	},
}).Parse(landingHTML))

// landingData is the view model for landing.html.
type landingData struct {
	Version    string
	StatusOK   bool
	Totals     mcp.CorpusTotals
	Frameworks []mcp.FrameworkVersionStatus
	EvalFloors []mcp.EvalFloor
}

// landingHandler serves the public landing page at GET / — project info, live
// corpus counts, and the build version — so an operator (or anyone) can see the
// instance is up and which frameworks are indexed without authenticating. It
// exposes only metadata (framework names + counts), never licensed control text,
// so it is safe to serve publicly. Reached through CloudFront like any other
// non-/healthz path; a direct-to-origin request is refused by origin-verify.
func landingHandler(version string, status func(context.Context) (mcp.CorpusStatusOutput, error), log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := landingData{Version: version, EvalFloors: mcp.EvalFloors()}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if out, err := status(ctx); err != nil {
			log.Warn("landing: corpus status unavailable", "err", err)
		} else {
			d.StatusOK = true
			d.Totals = out.Totals
			d.Frameworks = out.Frameworks
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		if err := landingTmpl.Execute(w, d); err != nil {
			log.Error("landing: render failed", "err", err)
		}
	}
}
