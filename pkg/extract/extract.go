// Package extract implements the extract pipeline stage: read eligible
// manifest rows, dispatch by file_format, upsert bronze.source_file +
// bronze.raw_extract, and mark rows extracted.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"danny.vn/compliary/pkg/manifest"
	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// Summary holds the result counters from an extract run.
type Summary struct {
	Succeeded int
	Failed    int
	Skipped   int
}

// IngestQuerier is the subset of dbingest.Querier needed by extract.
type IngestQuerier interface {
	MarkExtracted(ctx context.Context, id int64) error
	SetStageError(ctx context.Context, arg dbingest.SetStageErrorParams) error
}

// BronzeQuerier is the subset of dbbronze.Querier needed by extract.
type BronzeQuerier interface {
	UpsertSourceFile(ctx context.Context, arg dbbronze.UpsertSourceFileParams) (dbbronze.BronzeSourceFile, error)
	UpsertRawExtract(ctx context.Context, arg dbbronze.UpsertRawExtractParams) (int64, error)
}

// ConfigQuerier is the subset of dbconfig.Querier needed by extract.
type ConfigQuerier interface {
	GetFramework(ctx context.Context, code string) (dbconfig.ConfigFramework, error)
}

// Extractor runs the extract stage over a set of manifest rows.
type Extractor struct {
	DataDir string
	Rules   []manifest.Rule
	Log     *slog.Logger
}

// Run processes the given manifest files (already filtered to extract-eligible,
// extracted_at IS NULL). It dispatches by file_format: oscal-json, xlsx, pdf.
func (e *Extractor) Run(
	ctx context.Context,
	files []dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	cfgQ ConfigQuerier,
) (Summary, error) {
	var sum Summary

	for _, f := range files {
		format := deref(f.FileFormat)
		switch format {
		case "oscal-json":
			if err := e.extractOSCAL(ctx, f, ingQ, bronzeQ, cfgQ); err != nil {
				e.Log.Error("extract failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("extract: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "xlsx":
			if err := e.extractWorkbook(ctx, f, ingQ, bronzeQ, cfgQ); err != nil {
				e.Log.Error("extract failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("extract: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "pdf":
			if err := e.extractPDF(ctx, f, ingQ, bronzeQ, cfgQ); err != nil {
				e.Log.Error("extract failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("extract: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		default:
			// Unknown format — treat as error, not skip.
			e.Log.Error("unsupported file format", "path", f.RelPath, "format", format)
			_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
				ID:         f.ID,
				StageError: fmt.Sprintf("extract: unsupported format %q for %s", format, f.RelPath),
			})
			sum.Failed++
		}
	}

	return sum, nil
}

// extractOSCAL handles the oscal-json format: validate JSON, verify catalog
// envelope, upsert source_file with provenance, upsert raw_extract with the
// raw bytes, and mark extracted.
func (e *Extractor) extractOSCAL(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	cfgQ ConfigQuerier,
) error {
	absPath := filepath.Join(e.DataDir, f.RelPath)
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Validate JSON.
	if !json.Valid(raw) {
		return fmt.Errorf("invalid JSON in %s", f.RelPath)
	}

	// Verify top-level OSCAL catalog envelope.
	var envelope struct {
		Catalog struct {
			UUID     string `json:"uuid"`
			Metadata struct {
				Title        string `json:"title"`
				Version      string `json:"version"`
				OSCALVersion string `json:"oscal-version"`
			} `json:"metadata"`
			Groups []json.RawMessage `json:"groups"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("unmarshal catalog envelope: %w", err)
	}
	if envelope.Catalog.Metadata.Title == "" {
		return fmt.Errorf("missing catalog.metadata.title in %s", f.RelPath)
	}
	if envelope.Catalog.Groups == nil {
		return fmt.Errorf("missing catalog.groups in %s", f.RelPath)
	}

	// Resolve provenance from file_rule (re-match in-process from loaded rules).
	licenseKind := ""
	sourceURL := ""
	provenanceNote := ""
	if rule, ok := matchRule(e.Rules, f.RelPath); ok {
		licenseKind = deref(rule.LicenseKind)
		sourceURL = rule.SourceURL
		provenanceNote = rule.Provenance
	}

	// Resolve serve_gate from the framework's serve_policy.
	fwCode := deref(f.FrameworkCode)
	serveGate := "auth-only"
	if fwCode != "" {
		fw, err := cfgQ.GetFramework(ctx, fwCode)
		if err != nil {
			e.Log.Warn("cannot load framework for serve_gate", "code", fwCode, "err", err)
		} else if fw.ServePolicy == "full" {
			serveGate = "public"
		}
	}

	// Upsert bronze.source_file.
	sf, err := bronzeQ.UpsertSourceFile(ctx, dbbronze.UpsertSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
		FrameworkCode:   deref(f.FrameworkCode),
		VersionLabel:    deref(f.VersionLabel),
		DocRole:         deref(f.DocRole),
		FileFormat:      deref(f.FileFormat),
		SourceUrl:       sourceURL,
		LicenseKind:     licenseKind,
		RetrievedOn:     nil, // drop-in date unknown — honest NULL
		ProvenanceNote:  provenanceNote,
		ServeGate:       serveGate,
	})
	if err != nil {
		return fmt.Errorf("upsert source_file: %w", err)
	}

	// Upsert bronze.raw_extract: byte-preserving (no re-serialize).
	_, err = bronzeQ.UpsertRawExtract(ctx, dbbronze.UpsertRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "oscal-catalog-json",
		Content:      nil,
		ContentJsonb: json.RawMessage(raw),
	})
	if err != nil {
		return fmt.Errorf("upsert raw_extract: %w", err)
	}

	// Mark the manifest row as extracted.
	if err := ingQ.MarkExtracted(ctx, f.ID); err != nil {
		return fmt.Errorf("mark extracted: %w", err)
	}

	e.Log.Info("extracted",
		"path", f.RelPath,
		"catalog_title", envelope.Catalog.Metadata.Title,
		"groups", len(envelope.Catalog.Groups),
	)
	return nil
}

// matchRule finds the first matching Rule for the given relPath, using the
// same filepath.Match semantics as the manifest stage. Returns the Rule and
// true if found.
func matchRule(rules []manifest.Rule, relPath string) (manifest.Rule, bool) {
	for _, r := range rules {
		ok, err := filepath.Match(r.Pattern, relPath)
		if err != nil {
			continue
		}
		if ok {
			return r, true
		}
	}
	return manifest.Rule{}, false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
