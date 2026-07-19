package extract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"danny.vn/compliary/pkg/manifest"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// buildSyntheticXLSX creates a synthetic .xlsx file in dir with two sheets,
// a numeric cell, a shared string (repeated value), and empty cells (omitted).
func buildSyntheticXLSX(t *testing.T, dir, rel string) {
	t.Helper()
	xl := excelize.NewFile()
	defer func() { _ = xl.Close() }()

	// Sheet1 (default) — rename to "Data".
	idx, err := xl.GetSheetIndex("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	xl.SetSheetName(xl.GetSheetName(idx), "Data")

	// Fill cells: A1=Header, B1=Values, A2=42 (numeric), B2 empty (omit),
	// A3=shared (same as A1, exercises shared strings), B3=hello.
	xl.SetCellValue("Data", "A1", "Header")
	xl.SetCellValue("Data", "B1", "Values")
	xl.SetCellValue("Data", "A2", 42)
	// B2 intentionally empty.
	xl.SetCellValue("Data", "A3", "Header") // shared string (same value as A1)
	xl.SetCellValue("Data", "B3", "hello")

	// Second sheet "Meta" with one cell.
	_, err = xl.NewSheet("Meta")
	if err != nil {
		t.Fatal(err)
	}
	xl.SetCellValue("Meta", "C5", "metadata-value")

	// Write to disk.
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := xl.SaveAs(p); err != nil {
		t.Fatal(err)
	}
}

func TestExtractWorkbook_HappyPath(t *testing.T) {
	dir := t.TempDir()
	rel := "test/workbook.xlsx"
	buildSyntheticXLSX(t, dir, rel)

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "test/workbook.xlsx",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/workbook.xlsx",
			Provenance:    "synthetic test fixture",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       rel,
			Sha256:        "deadbeef",
			SizeBytes:     1000,
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			Status:        "active",
		},
	}

	bronzeQ := newFakeBronzeQuerier()
	ingestQ := newFakeIngestQuerier()
	configQ := newFakeConfigQuerier()

	ext := &Extractor{
		DataDir: dir,
		Rules:   rules,
		Log:     testLogger(),
	}
	sum, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Should succeed, not skip.
	if sum.Succeeded != 1 {
		t.Errorf("succeeded=%d, want 1", sum.Succeeded)
	}
	if sum.Skipped != 0 {
		t.Errorf("skipped=%d, want 0", sum.Skipped)
	}
	if sum.Failed != 0 {
		t.Errorf("failed=%d, want 0", sum.Failed)
	}

	// Check source_file upserted with correct provenance.
	if len(bronzeQ.sourceFiles) != 1 {
		t.Fatalf("source_files=%d, want 1", len(bronzeQ.sourceFiles))
	}
	for _, sf := range bronzeQ.sourceFiles {
		if sf.LicenseKind != "public-domain" {
			t.Errorf("license_kind=%q, want public-domain", sf.LicenseKind)
		}
		if sf.ServeGate != "public" {
			t.Errorf("serve_gate=%q, want public (synth fw has full serve_policy)", sf.ServeGate)
		}
		if sf.SourceUrl != "https://example.com/workbook.xlsx" {
			t.Errorf("source_url=%q", sf.SourceUrl)
		}
		if sf.ProvenanceNote != "synthetic test fixture" {
			t.Errorf("provenance_note=%q", sf.ProvenanceNote)
		}
	}

	// Check raw_extract.
	if len(bronzeQ.extracts) != 1 {
		t.Fatalf("raw_extracts=%d, want 1", len(bronzeQ.extracts))
	}
	for _, re := range bronzeQ.extracts {
		if re.Kind != "workbook-rows-json" {
			t.Errorf("kind=%q, want workbook-rows-json", re.Kind)
		}
		if re.Content != nil {
			t.Error("content should be nil (uses content_jsonb)")
		}
		if !json.Valid(re.ContentJsonb) {
			t.Fatal("content_jsonb is not valid JSON")
		}

		// Unmarshal and verify structure.
		var cap WorkbookCapture
		if err := json.Unmarshal(re.ContentJsonb, &cap); err != nil {
			t.Fatalf("unmarshal capture: %v", err)
		}

		// Should have 2 sheets: Data and Meta.
		if len(cap.Sheets) != 2 {
			t.Fatalf("sheets=%d, want 2", len(cap.Sheets))
		}
		if cap.Sheets[0].Name != "Data" {
			t.Errorf("sheet[0].name=%q, want Data", cap.Sheets[0].Name)
		}
		if cap.Sheets[1].Name != "Meta" {
			t.Errorf("sheet[1].name=%q, want Meta", cap.Sheets[1].Name)
		}

		// Data sheet should have 5 non-empty cells: A1, B1, A2, A3, B3.
		dataCells := cap.Sheets[0].Rows
		if len(dataCells) != 5 {
			t.Errorf("Data cells=%d, want 5; got %+v", len(dataCells), dataCells)
		}

		// Verify specific cells.
		cellMap := map[string]string{}
		for _, c := range dataCells {
			cellMap[c.Ref] = c.Value
		}
		if cellMap["A1"] != "Header" {
			t.Errorf("A1=%q, want Header", cellMap["A1"])
		}
		if cellMap["A2"] != "42" {
			t.Errorf("A2=%q, want '42' (numeric as string)", cellMap["A2"])
		}
		if cellMap["A3"] != "Header" {
			t.Errorf("A3=%q, want Header (shared string)", cellMap["A3"])
		}
		if cellMap["B3"] != "hello" {
			t.Errorf("B3=%q, want hello", cellMap["B3"])
		}
		if _, exists := cellMap["B2"]; exists {
			t.Error("B2 should be omitted (empty cell)")
		}

		// Meta sheet should have 1 cell: C5.
		metaCells := cap.Sheets[1].Rows
		if len(metaCells) != 1 {
			t.Errorf("Meta cells=%d, want 1; got %+v", len(metaCells), metaCells)
		}
		if len(metaCells) > 0 {
			if metaCells[0].Ref != "C5" || metaCells[0].Value != "metadata-value" {
				t.Errorf("Meta cell: got ref=%q val=%q, want C5/metadata-value", metaCells[0].Ref, metaCells[0].Value)
			}
		}
	}

	// Check manifest row marked extracted.
	if !ingestQ.extracted[1] {
		t.Error("manifest row 1 should be marked extracted")
	}
}

func TestExtractWorkbook_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	rel := "test/corrupt.xlsx"
	// Write garbage bytes (not a valid xlsx/zip).
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not a zip file at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "test/corrupt.xlsx",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/corrupt.xlsx",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            10,
			RelPath:       rel,
			Sha256:        "aaa",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			Status:        "active",
		},
	}

	bronzeQ := newFakeBronzeQuerier()
	ingestQ := newFakeIngestQuerier()
	configQ := newFakeConfigQuerier()

	ext := &Extractor{
		DataDir: dir,
		Rules:   rules,
		Log:     testLogger(),
	}
	sum, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract should not return fatal error: %v", err)
	}

	// Corrupt file should fail (stage_error + continue).
	if sum.Failed != 1 {
		t.Errorf("failed=%d, want 1", sum.Failed)
	}
	if sum.Succeeded != 0 {
		t.Errorf("succeeded=%d, want 0", sum.Succeeded)
	}

	// Should have stage_error set.
	if ingestQ.errors[10] == "" {
		t.Error("corrupt file should have stage_error set")
	}
	// Should NOT be marked extracted.
	if ingestQ.extracted[10] {
		t.Error("corrupt file should not be marked extracted")
	}
}

func TestExtractWorkbook_DeterministicOrder(t *testing.T) {
	// Verify that cells are in row-major order and sheets in workbook order.
	dir := t.TempDir()
	rel := "test/order.xlsx"

	// Build a workbook with cells in reverse order to verify sorting.
	xl := excelize.NewFile()
	defer func() { _ = xl.Close() }()

	idx, err := xl.GetSheetIndex("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	xl.SetSheetName(xl.GetSheetName(idx), "Second")

	// Create "First" sheet at index 0 (inserted before Second).
	firstIdx, err := xl.NewSheet("First")
	if err != nil {
		t.Fatal(err)
	}
	xl.SetActiveSheet(firstIdx)

	// Move "First" to be before "Second" in workbook order.
	// excelize.NewSheet appends; to get First before Second we set order.
	// Actually, the sheet list order is creation order. Let's rename.
	// Simpler: create with desired order by making Second after First.
	xl.DeleteSheet("Second")
	xl.SetSheetName(xl.GetSheetName(firstIdx), "First")
	_, err = xl.NewSheet("Second")
	if err != nil {
		t.Fatal(err)
	}

	// First: C1, A1, B1 — set in non-row-major order.
	xl.SetCellValue("First", "C1", "c")
	xl.SetCellValue("First", "A1", "a")
	xl.SetCellValue("First", "B1", "b")

	// Second: A2, A1.
	xl.SetCellValue("Second", "A2", "second-row")
	xl.SetCellValue("Second", "A1", "first-row")

	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := xl.SaveAs(p); err != nil {
		t.Fatal(err)
	}

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "test/order.xlsx",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/order.xlsx",
		},
	}
	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       rel,
			Sha256:        "abc",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("xlsx"),
			Status:        "active",
		},
	}

	bronzeQ := newFakeBronzeQuerier()
	ingestQ := newFakeIngestQuerier()
	configQ := newFakeConfigQuerier()

	ext := &Extractor{
		DataDir: dir,
		Rules:   rules,
		Log:     testLogger(),
	}
	sum, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if sum.Succeeded != 1 {
		t.Fatalf("succeeded=%d, want 1", sum.Succeeded)
	}

	// Verify order.
	for _, re := range bronzeQ.extracts {
		var cap WorkbookCapture
		if err := json.Unmarshal(re.ContentJsonb, &cap); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		// Sheets in workbook order: First, Second.
		if cap.Sheets[0].Name != "First" {
			t.Errorf("sheet[0]=%q, want First", cap.Sheets[0].Name)
		}
		if cap.Sheets[1].Name != "Second" {
			t.Errorf("sheet[1]=%q, want Second", cap.Sheets[1].Name)
		}

		// First sheet: row-major order → A1, B1, C1.
		first := cap.Sheets[0].Rows
		if len(first) != 3 {
			t.Fatalf("First cells=%d, want 3", len(first))
		}
		if first[0].Ref != "A1" || first[1].Ref != "B1" || first[2].Ref != "C1" {
			t.Errorf("First order: %v %v %v, want A1 B1 C1", first[0].Ref, first[1].Ref, first[2].Ref)
		}

		// Second sheet: row-major → A1, A2.
		second := cap.Sheets[1].Rows
		if len(second) != 2 {
			t.Fatalf("Second cells=%d, want 2", len(second))
		}
		if second[0].Ref != "A1" || second[1].Ref != "A2" {
			t.Errorf("Second order: %v %v, want A1 A2", second[0].Ref, second[1].Ref)
		}
	}
}
