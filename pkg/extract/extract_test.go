package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danny.vn/compliary/pkg/manifest"
	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// --- Synthetic OSCAL fixture ---

const syntheticOSCAL = `{
  "catalog": {
    "uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    "metadata": {
      "title": "Synthetic Catalog",
      "version": "1.0.0",
      "oscal-version": "1.2.2",
      "last-modified": "2024-01-01T00:00:00Z"
    },
    "groups": [
      {
        "id": "sy",
        "title": "Synthetic Family",
        "controls": [
          {
            "id": "sy-1",
            "title": "First Synthetic Control",
            "parts": [{"id": "sy-1_stmt", "name": "statement", "prose": "Organizations shall implement synthetic measures."}]
          },
          {
            "id": "sy-2",
            "title": "Second Synthetic Control",
            "controls": [
              {
                "id": "sy-2.1",
                "title": "Enhancement One",
                "parts": [{"id": "sy-2.1_stmt", "name": "statement", "prose": "Enhancement of the second control."}]
              }
            ]
          },
          {
            "id": "sy-3",
            "title": "Third Synthetic Control"
          }
        ]
      }
    ]
  }
}`

const invalidJSON = `{"catalog": {not valid json`

// --- Fakes ---

type fakeBronzeQuerier struct {
	sourceFiles map[string]dbbronze.BronzeSourceFile
	extracts    map[int64]dbbronze.UpsertRawExtractParams
	nextSFID    int64
}

func newFakeBronzeQuerier() *fakeBronzeQuerier {
	return &fakeBronzeQuerier{
		sourceFiles: map[string]dbbronze.BronzeSourceFile{},
		extracts:    map[int64]dbbronze.UpsertRawExtractParams{},
		nextSFID:    1,
	}
}

func (f *fakeBronzeQuerier) UpsertSourceFile(_ context.Context, arg dbbronze.UpsertSourceFileParams) (dbbronze.BronzeSourceFile, error) {
	key := arg.ManifestRelPath + "|" + arg.Sha256
	if existing, ok := f.sourceFiles[key]; ok {
		existing.FrameworkCode = arg.FrameworkCode
		existing.VersionLabel = arg.VersionLabel
		existing.DocRole = arg.DocRole
		existing.FileFormat = arg.FileFormat
		existing.SourceUrl = arg.SourceUrl
		existing.LicenseKind = arg.LicenseKind
		existing.RetrievedOn = arg.RetrievedOn
		existing.ProvenanceNote = arg.ProvenanceNote
		existing.ServeGate = arg.ServeGate
		f.sourceFiles[key] = existing
		return existing, nil
	}
	sf := dbbronze.BronzeSourceFile{
		ID:              f.nextSFID,
		ManifestRelPath: arg.ManifestRelPath,
		Sha256:          arg.Sha256,
		FrameworkCode:   arg.FrameworkCode,
		VersionLabel:    arg.VersionLabel,
		DocRole:         arg.DocRole,
		FileFormat:      arg.FileFormat,
		SourceUrl:       arg.SourceUrl,
		LicenseKind:     arg.LicenseKind,
		RetrievedOn:     arg.RetrievedOn,
		ProvenanceNote:  arg.ProvenanceNote,
		ServeGate:       arg.ServeGate,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	f.nextSFID++
	f.sourceFiles[key] = sf
	return sf, nil
}

func (f *fakeBronzeQuerier) UpsertRawExtract(_ context.Context, arg dbbronze.UpsertRawExtractParams) (int64, error) {
	f.extracts[arg.SourceFileID] = arg
	return arg.SourceFileID, nil
}

func (f *fakeBronzeQuerier) GetSourceFile(_ context.Context, _ dbbronze.GetSourceFileParams) (dbbronze.BronzeSourceFile, error) {
	return dbbronze.BronzeSourceFile{}, nil
}

func (f *fakeBronzeQuerier) GetRawExtract(_ context.Context, _ dbbronze.GetRawExtractParams) (dbbronze.BronzeRawExtract, error) {
	return dbbronze.BronzeRawExtract{}, nil
}

func (f *fakeBronzeQuerier) ListSourceFiles(_ context.Context) ([]dbbronze.BronzeSourceFile, error) {
	return nil, nil
}

type fakeIngestQuerier struct {
	extracted map[int64]bool
	errors    map[int64]string
}

func newFakeIngestQuerier() *fakeIngestQuerier {
	return &fakeIngestQuerier{
		extracted: map[int64]bool{},
		errors:    map[int64]string{},
	}
}

func (f *fakeIngestQuerier) MarkExtracted(_ context.Context, id int64) error {
	f.extracted[id] = true
	return nil
}

func (f *fakeIngestQuerier) SetStageError(_ context.Context, arg dbingest.SetStageErrorParams) error {
	f.errors[arg.ID] = arg.StageError
	return nil
}

// Stub remaining Querier methods (not used by extract).
func (f *fakeIngestQuerier) UpsertManifestFile(_ context.Context, _ dbingest.UpsertManifestFileParams) (dbingest.IngestManifestFile, error) {
	return dbingest.IngestManifestFile{}, nil
}
func (f *fakeIngestQuerier) DemoteMissingManifestFiles(_ context.Context, _ []string) (int64, error) {
	return 0, nil
}
func (f *fakeIngestQuerier) ListActiveManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) ListFilesToExtract(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) ListFilesToNormalize(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) ListFilesToIndex(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) ListIgnoredManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) ListUnrecognizedManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeIngestQuerier) MarkNormalized(_ context.Context, _ int64) error { return nil }
func (f *fakeIngestQuerier) MarkIndexed(_ context.Context, _ int64) error    { return nil }

type fakeConfigQuerier struct {
	frameworks map[string]dbconfig.ConfigFramework
}

func newFakeConfigQuerier() *fakeConfigQuerier {
	return &fakeConfigQuerier{
		frameworks: map[string]dbconfig.ConfigFramework{
			"synth": {
				Code:        "synth",
				ServePolicy: "full",
			},
			"restricted": {
				Code:        "restricted",
				ServePolicy: "authenticated",
			},
		},
	}
}

func (f *fakeConfigQuerier) GetFramework(_ context.Context, code string) (dbconfig.ConfigFramework, error) {
	fw, ok := f.frameworks[code]
	if !ok {
		return dbconfig.ConfigFramework{}, fmt.Errorf("framework %q not found", code)
	}
	return fw, nil
}

// --- Helpers ---

func strPtr(s string) *string { return &s }

func writeFixture(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// --- Tests ---

func TestExtract_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "nist/catalog.json", syntheticOSCAL)

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "nist/catalog.json",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/catalog.json",
			Provenance:    "synthetic test fixture",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       "nist/catalog.json",
			Sha256:        "deadbeef",
			SizeBytes:     int64(len(syntheticOSCAL)),
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
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

	// Check summary.
	if sum.Succeeded != 1 {
		t.Errorf("succeeded=%d, want 1", sum.Succeeded)
	}
	if sum.Failed != 0 {
		t.Errorf("failed=%d, want 0", sum.Failed)
	}
	if sum.Skipped != 0 {
		t.Errorf("skipped=%d, want 0", sum.Skipped)
	}

	// Check bronze.source_file was upserted.
	if len(bronzeQ.sourceFiles) != 1 {
		t.Fatalf("source_files=%d, want 1", len(bronzeQ.sourceFiles))
	}
	for _, sf := range bronzeQ.sourceFiles {
		if sf.LicenseKind != "public-domain" {
			t.Errorf("license_kind=%q, want public-domain", sf.LicenseKind)
		}
		if sf.ServeGate != "public" {
			t.Errorf("serve_gate=%q, want public", sf.ServeGate)
		}
		if sf.SourceUrl != "https://example.com/catalog.json" {
			t.Errorf("source_url=%q, want https://example.com/catalog.json", sf.SourceUrl)
		}
		if sf.ProvenanceNote != "synthetic test fixture" {
			t.Errorf("provenance_note=%q, want 'synthetic test fixture'", sf.ProvenanceNote)
		}
	}

	// Check bronze.raw_extract was upserted.
	if len(bronzeQ.extracts) != 1 {
		t.Fatalf("raw_extracts=%d, want 1", len(bronzeQ.extracts))
	}
	for _, re := range bronzeQ.extracts {
		if re.Kind != "oscal-catalog-json" {
			t.Errorf("kind=%q, want oscal-catalog-json", re.Kind)
		}
		if re.Content != nil {
			t.Error("content should be nil for OSCAL (uses content_jsonb)")
		}
		// Verify content_jsonb is valid JSON and byte-preserving.
		if !json.Valid(re.ContentJsonb) {
			t.Error("content_jsonb is not valid JSON")
		}
		// Should contain catalog metadata.
		if len(re.ContentJsonb) < 100 {
			t.Errorf("content_jsonb too small: %d bytes", len(re.ContentJsonb))
		}
	}

	// Check manifest row marked extracted.
	if !ingestQ.extracted[1] {
		t.Error("manifest row 1 should be marked extracted")
	}
}

func TestExtract_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "nist/bad.json", invalidJSON)
	writeFixture(t, dir, "nist/good.json", syntheticOSCAL)

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "nist/bad.json",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/bad.json",
		},
		{
			Ordinal:       200,
			Pattern:       "nist/good.json",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
			LicenseKind:   strPtr("public-domain"),
			SourceURL:     "https://example.com/good.json",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       "nist/bad.json",
			Sha256:        "aaa",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
			Status:        "active",
		},
		{
			ID:            2,
			RelPath:       "nist/good.json",
			Sha256:        "bbb",
			FrameworkCode: strPtr("synth"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
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

	// Bad file should fail, good file should succeed (continue-on-error).
	if sum.Failed != 1 {
		t.Errorf("failed=%d, want 1", sum.Failed)
	}
	if sum.Succeeded != 1 {
		t.Errorf("succeeded=%d, want 1", sum.Succeeded)
	}

	// Bad file should have stage_error, not be marked extracted.
	if ingestQ.errors[1] == "" {
		t.Error("bad file should have stage_error set")
	}
	if ingestQ.extracted[1] {
		t.Error("bad file should NOT be marked extracted")
	}

	// Good file should be marked extracted, no error.
	if !ingestQ.extracted[2] {
		t.Error("good file should be marked extracted")
	}
	if ingestQ.errors[2] != "" {
		t.Errorf("good file should have no error, got %q", ingestQ.errors[2])
	}
}

func TestExtract_SkipsXlsxAndPdf(t *testing.T) {
	dir := t.TempDir()

	rules := []manifest.Rule{
		{Ordinal: 100, Pattern: "a.xlsx", FrameworkCode: strPtr("ciscontrols"), VersionLabel: strPtr("v8.1"), DocRole: strPtr("main"), FileFormat: strPtr("xlsx")},
		{Ordinal: 200, Pattern: "b.pdf", FrameworkCode: strPtr("pcidss"), VersionLabel: strPtr("v4.0.1"), DocRole: strPtr("main"), FileFormat: strPtr("pdf")},
	}

	files := []dbingest.IngestManifestFile{
		{ID: 1, RelPath: "a.xlsx", FrameworkCode: strPtr("ciscontrols"), FileFormat: strPtr("xlsx"), Status: "active"},
		{ID: 2, RelPath: "b.pdf", FrameworkCode: strPtr("pcidss"), FileFormat: strPtr("pdf"), Status: "active"},
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

	if sum.Skipped != 2 {
		t.Errorf("skipped=%d, want 2", sum.Skipped)
	}
	if sum.Succeeded != 0 {
		t.Errorf("succeeded=%d, want 0", sum.Succeeded)
	}
	if sum.Failed != 0 {
		t.Errorf("failed=%d, want 0", sum.Failed)
	}

	// Skipped files should NOT be marked extracted or have errors.
	if ingestQ.extracted[1] || ingestQ.extracted[2] {
		t.Error("skipped files should not be marked extracted")
	}
	if ingestQ.errors[1] != "" || ingestQ.errors[2] != "" {
		t.Error("skipped files should not have stage_error")
	}
}

func TestExtract_ServeGateAuthOnly(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "doc.json", syntheticOSCAL)

	rules := []manifest.Rule{
		{
			Ordinal:       100,
			Pattern:       "doc.json",
			FrameworkCode: strPtr("restricted"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
			LicenseKind:   strPtr("purchased"),
			SourceURL:     "https://example.com/restricted.json",
		},
	}

	files := []dbingest.IngestManifestFile{
		{
			ID:            1,
			RelPath:       "doc.json",
			Sha256:        "xyz",
			FrameworkCode: strPtr("restricted"),
			VersionLabel:  strPtr("1.0"),
			DocRole:       strPtr("main"),
			FileFormat:    strPtr("oscal-json"),
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
	_, err := ext.Run(context.Background(), files, ingestQ, bronzeQ, configQ)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, sf := range bronzeQ.sourceFiles {
		if sf.ServeGate != "auth-only" {
			t.Errorf("serve_gate=%q, want auth-only for non-full serve_policy", sf.ServeGate)
		}
	}
}
