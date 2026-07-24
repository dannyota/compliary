package extract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"danny.vn/compliary/pkg/manifest"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// Invented, structure-alike fixtures — no real TCVN wording. Each uses the
// exact page-marker line form the extractor recognizes:
// "===== [trang N] =====".

// fixtureThreePages exercises marker splitting, page numbering, marker
// stripping, and verbatim whitespace preservation (leading + trailing blank
// lines around a page body are kept exactly).
const fixtureThreePages = "===== [trang 1] =====\n" +
	"\n" +
	"Alpha Control ALPHA-1\n" +
	"Requirement text line.\n" +
	"\n" +
	"===== [trang 2] =====\n" +
	"Beta Control BETA-2\n" +
	"===== [trang 3] =====\n" +
	"Gamma\n"

func TestSplitTxtPages_MarkersNumberingStripping(t *testing.T) {
	pages := splitTxtPages(fixtureThreePages)

	if len(pages) != 3 {
		t.Fatalf("pages=%d, want 3", len(pages))
	}

	wantN := []int{1, 2, 3}
	wantText := []string{
		"\nAlpha Control ALPHA-1\nRequirement text line.\n\n",
		"Beta Control BETA-2\n",
		"Gamma\n",
	}
	for i, p := range pages {
		if p.N != wantN[i] {
			t.Errorf("page[%d].N=%d, want %d", i, p.N, wantN[i])
		}
		if p.Text != wantText[i] {
			t.Errorf("page[%d].Text=%q, want %q", i, p.Text, wantText[i])
		}
		// Marker lines must never leak into stored page text.
		if strings.Contains(p.Text, "=====") {
			t.Errorf("page[%d].Text still contains a marker: %q", i, p.Text)
		}
	}
}

// Page numbers come from the marker, not a running counter: a gap in the
// marker sequence must be reflected verbatim.
func TestSplitTxtPages_NumberFromMarkerNotCounter(t *testing.T) {
	content := "===== [trang 1] =====\nfirst\n===== [trang 5] =====\nfifth\n"
	pages := splitTxtPages(content)

	if len(pages) != 2 {
		t.Fatalf("pages=%d, want 2", len(pages))
	}
	if pages[0].N != 1 || pages[0].Text != "first\n" {
		t.Errorf("page[0]={N:%d,Text:%q}, want {1,%q}", pages[0].N, pages[0].Text, "first\n")
	}
	if pages[1].N != 5 || pages[1].Text != "fifth\n" {
		t.Errorf("page[1]={N:%d,Text:%q}, want {5,%q}", pages[1].N, pages[1].Text, "fifth\n")
	}
}

// A marker immediately followed by another marker yields an empty page —
// tolerated, not dropped.
func TestSplitTxtPages_EmptyPage(t *testing.T) {
	content := "===== [trang 1] =====\n===== [trang 2] =====\ncontent\n"
	pages := splitTxtPages(content)

	if len(pages) != 2 {
		t.Fatalf("pages=%d, want 2", len(pages))
	}
	if pages[0].N != 1 || pages[0].Text != "" {
		t.Errorf("page[0]={N:%d,Text:%q}, want {1,\"\"}", pages[0].N, pages[0].Text)
	}
	if pages[1].N != 2 || pages[1].Text != "content\n" {
		t.Errorf("page[1]={N:%d,Text:%q}, want {2,%q}", pages[1].N, pages[1].Text, "content\n")
	}
}

// Text before the first marker belongs to page 1.
func TestSplitTxtPages_PreambleBeforeFirstMarker(t *testing.T) {
	content := "preamble line\n===== [trang 2] =====\nbody\n"
	pages := splitTxtPages(content)

	if len(pages) != 2 {
		t.Fatalf("pages=%d, want 2", len(pages))
	}
	if pages[0].N != 1 || pages[0].Text != "preamble line\n" {
		t.Errorf("page[0]={N:%d,Text:%q}, want {1,%q}", pages[0].N, pages[0].Text, "preamble line\n")
	}
	if pages[1].N != 2 || pages[1].Text != "body\n" {
		t.Errorf("page[1]={N:%d,Text:%q}, want {2,%q}", pages[1].N, pages[1].Text, "body\n")
	}
}

// A document with no markers at all is a single page 1 holding the whole text.
func TestSplitTxtPages_NoMarkers(t *testing.T) {
	content := "just text\nno markers\n"
	pages := splitTxtPages(content)

	if len(pages) != 1 {
		t.Fatalf("pages=%d, want 1", len(pages))
	}
	if pages[0].N != 1 || pages[0].Text != content {
		t.Errorf("page[0]={N:%d,Text:%q}, want {1,%q}", pages[0].N, pages[0].Text, content)
	}
}

func TestExtractTXT_HappyPath(t *testing.T) {
	dir := t.TempDir()
	rel := "tcvn/doc.txt"
	writeFixture(t, dir, rel, fixtureThreePages)

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "tcvn/doc.txt",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("txt"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/doc.txt",
			Provenance:    "synthetic test fixture",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       rel,
			Sha256:        "deadbeef",
			SizeBytes:     int64(len(fixtureThreePages)),
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("txt"),
			Status:        "active",
		},
	}

	bronzeQ := newFakeBronzeQuerier()
	ingestQ := newFakeIngestQuerier()
	configQ := newFakeConfigQuerier()

	ext := &Extractor{DataDir: dir, Rules: rules, Log: testLogger()}
	sum, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if sum.Succeeded != 1 || sum.Failed != 0 || sum.Skipped != 0 {
		t.Errorf("summary=%+v, want {1 0 0}", sum)
	}

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
		if sf.SourceUrl != "https://example.com/doc.txt" {
			t.Errorf("source_url=%q", sf.SourceUrl)
		}
		if sf.ProvenanceNote != "synthetic test fixture" {
			t.Errorf("provenance_note=%q", sf.ProvenanceNote)
		}
		if sf.FileFormat != "txt" {
			t.Errorf("file_format=%q, want txt", sf.FileFormat)
		}
	}

	if len(bronzeQ.extracts) != 1 {
		t.Fatalf("raw_extracts=%d, want 1", len(bronzeQ.extracts))
	}
	for _, re := range bronzeQ.extracts {
		if re.Kind != "txt-pages-json" {
			t.Errorf("kind=%q, want txt-pages-json", re.Kind)
		}
		if re.Content != nil {
			t.Error("content should be nil (uses content_jsonb)")
		}
		if !json.Valid(re.ContentJsonb) {
			t.Fatal("content_jsonb is not valid JSON")
		}
		var cap TxtCapture
		if err := json.Unmarshal(re.ContentJsonb, &cap); err != nil {
			t.Fatalf("unmarshal capture: %v", err)
		}
		if len(cap.Pages) != 3 {
			t.Fatalf("pages=%d, want 3", len(cap.Pages))
		}
		if cap.Pages[0].N != 1 || cap.Pages[2].N != 3 {
			t.Errorf("page numbers=%d..%d, want 1..3", cap.Pages[0].N, cap.Pages[2].N)
		}
	}

	if !ingestQ.extracted[1] {
		t.Error("manifest row 1 should be marked extracted")
	}
}

func TestExtractTXT_MissingFile(t *testing.T) {
	dir := t.TempDir()

	rules := []manifest.Rule{
		{Ordinal: 100, Pattern: "tcvn/missing.txt", FrameworkCode: strPtr("synth"), VersionLabel: strPtr("1.0"), DocRole: strPtr("main"), FileFormat: strPtr("txt")},
	}
	files := []dbingest.IngestManifestFile{
		{ID: 7, RelPath: "tcvn/missing.txt", FrameworkCode: strPtr("synth"), FileFormat: strPtr("txt"), Status: "active"},
	}

	bronzeQ := newFakeBronzeQuerier()
	ingestQ := newFakeIngestQuerier()
	configQ := newFakeConfigQuerier()

	ext := &Extractor{DataDir: dir, Rules: rules, Log: testLogger()}
	sum, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract should not return fatal error: %v", err)
	}
	if sum.Failed != 1 || sum.Succeeded != 0 {
		t.Errorf("summary=%+v, want failed=1 succeeded=0", sum)
	}
	if ingestQ.errors[7] == "" {
		t.Error("missing txt should have stage_error set")
	}
	if ingestQ.extracted[7] {
		t.Error("missing txt should not be marked extracted")
	}
}
