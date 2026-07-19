package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/xuri/excelize/v2"

	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// WorkbookCapture is the top-level JSON structure stored in
// bronze.raw_extract.content_jsonb for kind 'workbook-rows-json'.
type WorkbookCapture struct {
	Sheets []SheetCapture `json:"sheets"`
}

// SheetCapture holds a single sheet's cell data.
type SheetCapture struct {
	Name string        `json:"name"`
	Rows []CellCapture `json:"rows"`
}

// CellCapture holds a single cell's reference and formatted string value.
type CellCapture struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

// extractWorkbook handles the 'xlsx' format: open the workbook, capture every
// sheet's cells as workbook-rows-json, upsert source_file + raw_extract, mark
// extracted.
func (e *Extractor) extractWorkbook(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	cfgQ ConfigQuerier,
) error {
	absPath := filepath.Join(e.DataDir, f.RelPath)

	// Open the xlsx file.
	xlFile, err := excelize.OpenFile(absPath)
	if err != nil {
		return fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = xlFile.Close() }()

	// Capture all sheets in workbook order.
	capture, err := captureWorkbook(xlFile)
	if err != nil {
		return fmt.Errorf("capture workbook: %w", err)
	}

	// Marshal the capture to JSON.
	captureJSON, err := json.Marshal(capture)
	if err != nil {
		return fmt.Errorf("marshal capture: %w", err)
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

	// Upsert bronze.raw_extract with workbook-rows-json capture.
	_, err = bronzeQ.UpsertRawExtract(ctx, dbbronze.UpsertRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "workbook-rows-json",
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

	e.Log.Info("extracted xlsx",
		"path", f.RelPath,
		"sheets", len(capture.Sheets),
		"json_bytes", len(captureJSON),
	)
	return nil
}

// CaptureXLSXFile opens an xlsx file from disk and returns the exact
// workbook-rows-json capture (as json.RawMessage) that the extract stage
// would store in bronze. This is exported for test-time use by parser
// golden tests that must build captures from data/ without committing
// licensed captures to testdata/.
func CaptureXLSXFile(path string) (json.RawMessage, error) {
	xlFile, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx %q: %w", path, err)
	}
	defer func() { _ = xlFile.Close() }()

	capture, err := captureWorkbook(xlFile)
	if err != nil {
		return nil, fmt.Errorf("capture workbook: %w", err)
	}

	raw, err := json.Marshal(capture)
	if err != nil {
		return nil, fmt.Errorf("marshal capture: %w", err)
	}
	return json.RawMessage(raw), nil
}

// captureWorkbook reads all sheets in workbook order and returns the cell
// capture. Empty cells are omitted. Cell order is row-major within each sheet.
// Uses excelize.GetRows for each sheet — it resolves shared strings and
// returns formatted values, which gives us the deterministic row-major order
// we need (sheet order = workbook order, cell order = row-major).
func captureWorkbook(xl *excelize.File) (*WorkbookCapture, error) {
	sheetList := xl.GetSheetList()
	capture := &WorkbookCapture{
		Sheets: make([]SheetCapture, 0, len(sheetList)),
	}

	for _, name := range sheetList {
		rows, err := xl.GetRows(name, excelize.Options{RawCellValue: false})
		if err != nil {
			return nil, fmt.Errorf("get rows for sheet %q: %w", name, err)
		}

		sheet := SheetCapture{Name: name, Rows: captureFromGetRows(rows)}
		capture.Sheets = append(capture.Sheets, sheet)
	}

	return capture, nil
}

// captureFromGetRows converts excelize.GetRows output (2D jagged string
// slice) into CellCapture entries, building cell refs from indices. Empty
// values are omitted. The result is inherently row-major (row 1 left-to-right,
// then row 2, …).
func captureFromGetRows(rows [][]string) []CellCapture {
	var cells []CellCapture
	for rowIdx, row := range rows {
		for colIdx, val := range row {
			if val == "" {
				continue
			}
			cellName, err := excelize.CoordinatesToCellName(colIdx+1, rowIdx+1)
			if err != nil {
				continue
			}
			cells = append(cells, CellCapture{Ref: cellName, Value: val})
		}
	}
	return cells
}
