package extract

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"danny.vn/compliary/pkg/manifest"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// buildSyntheticPDF writes a minimal valid PDF to dir/rel containing the
// given text on a single page. The PDF is hand-crafted (no library needed)
// and is deliberately minimal — go-fitz can open it and extract text.
func buildSyntheticPDF(t *testing.T, dir, rel, text string) {
	t.Helper()

	// Minimal PDF 1.4 with one page, one text object.
	// Font is the built-in Helvetica (no embed needed).
	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"

	pdf := "%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]" +
		"/Contents 4 0 R/Resources<</Font<</F1<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>>>>>>>endobj\n" +
		"4 0 obj<</Length " + itoa(len(stream)) + ">>\nstream\n" + stream + "\nendstream\nendobj\n" +
		"xref\n0 5\n" +
		"0000000000 65535 f \n" +
		"0000000009 00000 n \n" +
		"0000000058 00000 n \n" +
		"0000000115 00000 n \n" +
		"0000000314 00000 n \n" +
		"trailer<</Size 5/Root 1 0 R>>\n" +
		"startxref\n9\n%%EOF\n"

	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(pdf), 0o644); err != nil {
		t.Fatal(err)
	}
}

// itoa converts an int to a decimal string (avoids importing strconv for a test helper).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestExtractPDF_HappyPath(t *testing.T) {
	dir := t.TempDir()
	rel := "test/doc.pdf"
	buildSyntheticPDF(t, dir, rel, "Synthetic control requirement SC-1")

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "test/doc.pdf",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("pdf"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/doc.pdf",
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
			FileFormat:    strPtr("pdf"),
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
		if sf.SourceUrl != "https://example.com/doc.pdf" {
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
		if re.Kind != "pdf-pages-json" {
			t.Errorf("kind=%q, want pdf-pages-json", re.Kind)
		}
		if re.Content != nil {
			t.Error("content should be nil (uses content_jsonb)")
		}
		if !json.Valid(re.ContentJsonb) {
			t.Fatal("content_jsonb is not valid JSON")
		}

		var cap PDFCapture
		if err := json.Unmarshal(re.ContentJsonb, &cap); err != nil {
			t.Fatalf("unmarshal capture: %v", err)
		}

		if len(cap.Pages) != 1 {
			t.Fatalf("pages=%d, want 1", len(cap.Pages))
		}
		if cap.Pages[0].N != 1 {
			t.Errorf("page.n=%d, want 1", cap.Pages[0].N)
		}
		// The synthetic PDF text should be recoverable.
		if cap.Pages[0].Text == "" {
			t.Error("page text is empty — go-fitz did not extract text")
		}
	}

	// Check manifest row marked extracted.
	if !ingestQ.extracted[1] {
		t.Error("manifest row 1 should be marked extracted")
	}
}

func TestExtractPDF_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	rel := "test/corrupt.pdf"
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("not a pdf file at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "test/corrupt.pdf",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("pdf"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/corrupt.pdf",
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
			FileFormat:    strPtr("pdf"),
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

	if sum.Failed != 1 {
		t.Errorf("failed=%d, want 1", sum.Failed)
	}
	if sum.Succeeded != 0 {
		t.Errorf("succeeded=%d, want 0", sum.Succeeded)
	}
	if ingestQ.errors[10] == "" {
		t.Error("corrupt file should have stage_error set")
	}
	if ingestQ.extracted[10] {
		t.Error("corrupt file should not be marked extracted")
	}
}

func TestCapturePDFFile_Synthetic(t *testing.T) {
	dir := t.TempDir()
	rel := "capture-test.pdf"
	buildSyntheticPDF(t, dir, rel, "Test capture content")

	raw, err := CapturePDFFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("CapturePDFFile: %v", err)
	}

	if !json.Valid(raw) {
		t.Fatal("result is not valid JSON")
	}

	var cap PDFCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cap.Pages) != 1 {
		t.Fatalf("pages=%d, want 1", len(cap.Pages))
	}
	if cap.Pages[0].N != 1 {
		t.Errorf("page.n=%d, want 1", cap.Pages[0].N)
	}
}

func TestCapturePDFFile_NonExistent(t *testing.T) {
	_, err := CapturePDFFile("/nonexistent/path.pdf")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}
