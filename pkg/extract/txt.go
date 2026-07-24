package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// TxtCapture is the top-level JSON structure stored in
// bronze.raw_extract.content_jsonb for kind 'txt-pages-json'. It mirrors
// PDFCapture (same page shape) so a page-marked text document lands in bronze
// exactly like a per-page PDF capture.
type TxtCapture struct {
	Pages []PageCapture `json:"pages"`
}

// txtPageMarker matches a page-boundary line of the exact form
// "===== [trang N] =====" occupying a whole line. Marker lines are boundaries,
// not content: they are stripped from the stored page text and the captured N
// becomes the page's provenance number.
var txtPageMarker = regexp.MustCompile(`(?m)^===== \[trang (\d+)\] =====$`)

// extractTXT handles the 'txt' format: read the file as UTF-8, split on
// page-marker lines into per-page text (marker lines stripped, all other
// whitespace preserved verbatim), upsert source_file + raw_extract as
// txt-pages-json, and mark the manifest row extracted.
func (e *Extractor) extractTXT(
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

	captureJSON, pageCount, err := captureTXT(raw)
	if err != nil {
		return fmt.Errorf("capture txt: %w", err)
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

	// Upsert bronze.raw_extract with txt-pages-json capture.
	_, err = bronzeQ.UpsertRawExtract(ctx, dbbronze.UpsertRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "txt-pages-json",
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

	e.Log.Info("extracted txt",
		"path", f.RelPath,
		"pages", pageCount,
		"json_bytes", len(captureJSON),
	)
	return nil
}

// captureTXT splits page-marked UTF-8 text into per-page captures and returns
// the marshaled txt-pages-json capture with its page count.
func captureTXT(raw []byte) ([]byte, int, error) {
	capture := TxtCapture{Pages: splitTxtPages(string(raw))}
	out, err := json.Marshal(capture)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal capture: %w", err)
	}
	return out, len(capture.Pages), nil
}

// splitTxtPages divides content on page-marker lines. Each marker's N becomes
// the page number; the marker line (including its trailing newline) is
// stripped, and all other whitespace/newlines are preserved verbatim. Text
// before the first marker belongs to page 1; a document with no markers is a
// single page 1 holding the whole text.
func splitTxtPages(content string) []PageCapture {
	locs := txtPageMarker.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return []PageCapture{{N: 1, Text: content}}
	}

	pages := make([]PageCapture, 0, len(locs)+1)

	// Text before the first marker (if any) belongs to page 1.
	if locs[0][0] > 0 {
		pages = append(pages, PageCapture{N: 1, Text: content[:locs[0][0]]})
	}

	for i, loc := range locs {
		markerEnd := loc[1] // just after "=====", before the marker line's newline
		pageNum, _ := strconv.Atoi(content[loc[2]:loc[3]])

		// The marker line owns its trailing newline; skip it so the page text
		// is the verbatim content that follows the marker line.
		segStart := markerEnd
		if segStart < len(content) && content[segStart] == '\n' {
			segStart++
		}

		segEnd := len(content)
		if i+1 < len(locs) {
			segEnd = locs[i+1][0]
		}

		pages = append(pages, PageCapture{N: pageNum, Text: content[segStart:segEnd]})
	}

	return pages
}
