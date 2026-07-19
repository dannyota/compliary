package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"

	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// --- Fakes ---

type fakeIngestQuerier struct {
	normalized map[int64]bool
	errors     map[int64]string
}

func newFakeIngestQuerier() *fakeIngestQuerier {
	return &fakeIngestQuerier{
		normalized: map[int64]bool{},
		errors:     map[int64]string{},
	}
}

func (f *fakeIngestQuerier) MarkNormalized(_ context.Context, id int64) error {
	f.normalized[id] = true
	return nil
}

func (f *fakeIngestQuerier) SetStageError(_ context.Context, arg dbingest.SetStageErrorParams) error {
	f.errors[arg.ID] = arg.StageError
	return nil
}

type fakeBronzeQuerier struct {
	sourceFiles map[string]dbbronze.BronzeSourceFile
	extracts    map[int64]dbbronze.BronzeRawExtract
}

func newFakeBronzeQuerier() *fakeBronzeQuerier {
	return &fakeBronzeQuerier{
		sourceFiles: map[string]dbbronze.BronzeSourceFile{},
		extracts:    map[int64]dbbronze.BronzeRawExtract{},
	}
}

func (f *fakeBronzeQuerier) GetSourceFile(_ context.Context, arg dbbronze.GetSourceFileParams) (dbbronze.BronzeSourceFile, error) {
	key := arg.ManifestRelPath + "|" + arg.Sha256
	sf, ok := f.sourceFiles[key]
	if !ok {
		return dbbronze.BronzeSourceFile{}, fmt.Errorf("source file not found: %s", key)
	}
	return sf, nil
}

func (f *fakeBronzeQuerier) GetRawExtract(_ context.Context, arg dbbronze.GetRawExtractParams) (dbbronze.BronzeRawExtract, error) {
	re, ok := f.extracts[arg.SourceFileID]
	if !ok {
		return dbbronze.BronzeRawExtract{}, fmt.Errorf("raw extract not found: %d", arg.SourceFileID)
	}
	return re, nil
}

type fakeSilverQuerier struct {
	doc      *dbsilver.SilverDocument
	controls []dbsilver.InsertControlParams
	mappings []dbsilver.UpsertControlMappingParams
	nextID   int64
}

func newFakeSilverQuerier() *fakeSilverQuerier {
	return &fakeSilverQuerier{nextID: 1}
}

func (f *fakeSilverQuerier) UpsertDocument(_ context.Context, arg dbsilver.UpsertDocumentParams) (dbsilver.SilverDocument, error) {
	doc := dbsilver.SilverDocument{
		ID:               f.nextID,
		DocKey:           arg.DocKey,
		FrameworkCode:    arg.FrameworkCode,
		VersionLabel:     arg.VersionLabel,
		DocRole:          arg.DocRole,
		Qualifier:        arg.Qualifier,
		Title:            arg.Title,
		SourceFileSha256: arg.SourceFileSha256,
		ServeGate:        arg.ServeGate,
	}
	f.nextID++
	f.doc = &doc
	return doc, nil
}

func (f *fakeSilverQuerier) DeleteControlsForDocument(_ context.Context, _ int64) (int64, error) {
	f.controls = nil
	f.mappings = nil
	return 0, nil
}

func (f *fakeSilverQuerier) InsertControl(_ context.Context, arg dbsilver.InsertControlParams) (int64, error) {
	f.controls = append(f.controls, arg)
	id := f.nextID
	f.nextID++
	return id, nil
}

func (f *fakeSilverQuerier) UpsertControlMapping(_ context.Context, arg dbsilver.UpsertControlMappingParams) error {
	f.mappings = append(f.mappings, arg)
	return nil
}

func (f *fakeSilverQuerier) ResolveControlMappings(_ context.Context) (int64, error) {
	return 0, nil
}

type fakeConfigQuerier struct {
	frameworks map[string]dbconfig.ConfigFramework
}

func newFakeConfigQuerier() *fakeConfigQuerier {
	return &fakeConfigQuerier{
		frameworks: map[string]dbconfig.ConfigFramework{
			"nist80053": {
				Code:           "nist80053",
				CitationScheme: "oscal-catalog",
				ServePolicy:    "full",
			},
			"nistcsf": {
				Code:           "nistcsf",
				CitationScheme: "csf-workbook",
				ServePolicy:    "full",
			},
			"ciscontrols": {
				Code:           "ciscontrols",
				CitationScheme: "cis-xlsx",
				ServePolicy:    "authenticated",
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

// --- Tests ---

func TestNormalizer_HappyPath(t *testing.T) {
	bronzeQ := newFakeBronzeQuerier()
	bronzeQ.sourceFiles["nist/catalog.json|sha123"] = dbbronze.BronzeSourceFile{
		ID:        1,
		ServeGate: "public",
	}
	bronzeQ.extracts[1] = dbbronze.BronzeRawExtract{
		ID:           1,
		SourceFileID: 1,
		Kind:         "oscal-catalog-json",
		ContentJsonb: json.RawMessage(syntheticOSCAL),
	}

	ingestQ := newFakeIngestQuerier()
	silverQ := newFakeSilverQuerier()
	configQ := newFakeConfigQuerier()

	files := []dbingest.IngestManifestFile{
		{
			ID:            10,
			RelPath:       "nist/catalog.json",
			Sha256:        "sha123",
			FrameworkCode: strPtr("nist80053"),
			VersionLabel:  strPtr("r5"),
			DocRole:       strPtr("main"),
			Qualifier:     "",
		},
	}

	norm := &Normalizer{Log: testLogger()}
	sum, err := norm.Run(context.Background(), files, ingestQ, bronzeQ, silverQ, configQ)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if sum.Succeeded != 1 {
		t.Errorf("succeeded=%d, want 1", sum.Succeeded)
	}
	if sum.Failed != 0 {
		t.Errorf("failed=%d, want 0", sum.Failed)
	}

	// Verify document was created.
	if silverQ.doc == nil {
		t.Fatal("document not created")
	}
	if silverQ.doc.DocKey != "nist80053|r5|main" {
		t.Errorf("doc_key=%q, want nist80053|r5|main", silverQ.doc.DocKey)
	}
	if silverQ.doc.ServeGate != "public" {
		t.Errorf("serve_gate=%q, want public", silverQ.doc.ServeGate)
	}

	// Verify controls inserted (6 from synthetic fixture).
	if len(silverQ.controls) != 6 {
		t.Errorf("controls=%d, want 6", len(silverQ.controls))
	}

	// Verify mappings inserted.
	if len(silverQ.mappings) != 2 {
		t.Errorf("mappings=%d, want 2", len(silverQ.mappings))
	}

	// Verify MarkNormalized called.
	if !ingestQ.normalized[10] {
		t.Error("manifest row 10 should be marked normalized")
	}
}

func TestNormalizer_CSF_HappyPath(t *testing.T) {
	bronzeQ := newFakeBronzeQuerier()
	bronzeQ.sourceFiles["nist/nist-csf-2.0.xlsx|sha456"] = dbbronze.BronzeSourceFile{
		ID:        2,
		ServeGate: "public",
	}
	bronzeQ.extracts[2] = dbbronze.BronzeRawExtract{
		ID:           2,
		SourceFileID: 2,
		Kind:         "workbook-rows-json",
		ContentJsonb: json.RawMessage(syntheticCSF),
	}

	ingestQ := newFakeIngestQuerier()
	silverQ := newFakeSilverQuerier()
	configQ := newFakeConfigQuerier()

	files := []dbingest.IngestManifestFile{
		{
			ID:            30,
			RelPath:       "nist/nist-csf-2.0.xlsx",
			Sha256:        "sha456",
			FrameworkCode: strPtr("nistcsf"),
			VersionLabel:  strPtr("2.0"),
			DocRole:       strPtr("main"),
			Qualifier:     "",
		},
	}

	norm := &Normalizer{Log: testLogger()}
	sum, err := norm.Run(context.Background(), files, ingestQ, bronzeQ, silverQ, configQ)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if sum.Succeeded != 1 {
		t.Errorf("succeeded=%d, want 1", sum.Succeeded)
	}
	if sum.Failed != 0 {
		t.Errorf("failed=%d, want 0", sum.Failed)
	}

	// Verify document was created.
	if silverQ.doc == nil {
		t.Fatal("document not created")
	}
	if silverQ.doc.DocKey != "nistcsf|2.0|main" {
		t.Errorf("doc_key=%q, want nistcsf|2.0|main", silverQ.doc.DocKey)
	}
	if silverQ.doc.ServeGate != "public" {
		t.Errorf("serve_gate=%q, want public", silverQ.doc.ServeGate)
	}

	// Verify controls inserted (10 from synthetic CSF fixture).
	if len(silverQ.controls) != 10 {
		t.Errorf("controls=%d, want 10", len(silverQ.controls))
	}

	// Verify mappings inserted (5 edges).
	if len(silverQ.mappings) != 5 {
		t.Errorf("mappings=%d, want 5", len(silverQ.mappings))
	}

	// Verify MarkNormalized called.
	if !ingestQ.normalized[30] {
		t.Error("manifest row 30 should be marked normalized")
	}
}

func TestNormalizer_SkipsUnimplementedScheme(t *testing.T) {
	ingestQ := newFakeIngestQuerier()
	bronzeQ := newFakeBronzeQuerier()
	silverQ := newFakeSilverQuerier()
	configQ := newFakeConfigQuerier()

	files := []dbingest.IngestManifestFile{
		{
			ID:            20,
			RelPath:       "cis/controls.xlsx",
			Sha256:        "xxx",
			FrameworkCode: strPtr("ciscontrols"),
			VersionLabel:  strPtr("v8.1"),
			DocRole:       strPtr("main"),
		},
	}

	norm := &Normalizer{Log: testLogger()}
	sum, err := norm.Run(context.Background(), files, ingestQ, bronzeQ, silverQ, configQ)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if sum.Skipped != 1 {
		t.Errorf("skipped=%d, want 1", sum.Skipped)
	}
	if sum.Succeeded != 0 {
		t.Errorf("succeeded=%d, want 0", sum.Succeeded)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
