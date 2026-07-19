package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/gen2brain/go-fitz"

	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// PDFCapture is the top-level JSON structure stored in
// bronze.raw_extract.content_jsonb for kind 'pdf-pages-json'.
type PDFCapture struct {
	Pages []PageCapture `json:"pages"`
}

// PageCapture holds a single page's extracted text.
type PageCapture struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// extractPDF handles the 'pdf' format: open with go-fitz, extract per-page
// text into pdf-pages-json, upsert source_file + raw_extract, mark extracted.
func (e *Extractor) extractPDF(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	cfgQ ConfigQuerier,
) error {
	absPath := filepath.Join(e.DataDir, f.RelPath)

	captureJSON, err := capturePDF(absPath)
	if err != nil {
		return fmt.Errorf("capture pdf: %w", err)
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
		RetrievedOn:     nil,
		ProvenanceNote:  provenanceNote,
		ServeGate:       serveGate,
	})
	if err != nil {
		return fmt.Errorf("upsert source_file: %w", err)
	}

	// Upsert bronze.raw_extract with pdf-pages-json capture.
	_, err = bronzeQ.UpsertRawExtract(ctx, dbbronze.UpsertRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
		Content:      nil,
		ContentJsonb: json.RawMessage(captureJSON),
	})
	if err != nil {
		return fmt.Errorf("upsert raw_extract: %w", err)
	}

	// Mark the manifest row as extracted.
	if err := ingQ.MarkExtracted(ctx, f.ID); err != nil {
		return fmt.Errorf("mark extracted: %w", err)
	}

	// Count pages for logging.
	var cap PDFCapture
	_ = json.Unmarshal(captureJSON, &cap)

	e.Log.Info("extracted pdf",
		"path", f.RelPath,
		"pages", len(cap.Pages),
		"json_bytes", len(captureJSON),
	)
	return nil
}

// CapturePDFFile opens a PDF file from disk and returns the exact
// pdf-pages-json capture (as json.RawMessage) that the extract stage would
// store in bronze. Exported for test-time use by parser golden tests that
// must build captures from data/ without committing licensed captures to
// testdata/.
func CapturePDFFile(path string) (json.RawMessage, error) {
	raw, err := capturePDF(path)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// capturePDF opens a PDF file with go-fitz, extracts per-page text, and
// returns the marshaled pdf-pages-json capture.
func capturePDF(path string) ([]byte, error) {
	doc, err := fitz.New(path)
	if err != nil {
		return nil, fmt.Errorf("open pdf %q: %w", path, err)
	}
	defer doc.Close()

	numPages := doc.NumPage()
	capture := PDFCapture{
		Pages: make([]PageCapture, 0, numPages),
	}

	for i := 0; i < numPages; i++ {
		text, err := doc.Text(i)
		if err != nil {
			return nil, fmt.Errorf("extract text page %d: %w", i+1, err)
		}
		capture.Pages = append(capture.Pages, PageCapture{
			N:    i + 1,
			Text: text,
		})
	}

	raw, err := json.Marshal(capture)
	if err != nil {
		return nil, fmt.Errorf("marshal capture: %w", err)
	}
	return raw, nil
}
