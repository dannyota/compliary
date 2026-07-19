package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbingest "danny.vn/compliary/pkg/store/ingest"
)

func strPtr(s string) *string { return &s }

// --- Matcher tests ---

func TestMatch_FirstRuleWins(t *testing.T) {
	rules := []Rule{
		{Ordinal: 100, Pattern: "nist/*.json", FrameworkCode: strPtr("nist80053"), VersionLabel: strPtr("r5"), DocRole: strPtr("main"), FileFormat: strPtr("oscal-json")},
		{Ordinal: 200, Pattern: "nist/*", FrameworkCode: strPtr("nistcsf"), VersionLabel: strPtr("2.0"), DocRole: strPtr("main"), FileFormat: strPtr("xlsx")},
	}
	m := Match(rules, "nist/catalog.json")
	if !m.matched {
		t.Fatal("expected match")
	}
	if deref(m.frameworkCode) != "nist80053" {
		t.Errorf("got framework %q, want nist80053", deref(m.frameworkCode))
	}
}

func TestMatch_IgnoreRule(t *testing.T) {
	rules := []Rule{
		{Ordinal: 100, Pattern: "README.md", Ignore: true, IgnoreReason: "corpus readme"},
	}
	m := Match(rules, "README.md")
	if !m.matched {
		t.Fatal("expected match")
	}
	if !m.ignore {
		t.Error("expected ignore=true")
	}
	if m.ignoreReason != "corpus readme" {
		t.Errorf("got reason %q, want 'corpus readme'", m.ignoreReason)
	}
}

func TestMatch_NoMatch(t *testing.T) {
	rules := []Rule{
		{Ordinal: 100, Pattern: "nist/*.json", FrameworkCode: strPtr("nist80053"), VersionLabel: strPtr("r5"), DocRole: strPtr("main"), FileFormat: strPtr("oscal-json")},
	}
	m := Match(rules, "unknown/file.pdf")
	if m.matched {
		t.Error("expected no match")
	}
}

func TestMatch_ExactPath(t *testing.T) {
	rules := []Rule{
		{Ordinal: 100, Pattern: "csa/csa-ccm-v4.1.0.xlsx", FrameworkCode: strPtr("csaccm"), VersionLabel: strPtr("v4.1"), DocRole: strPtr("main"), FileFormat: strPtr("xlsx")},
	}
	m := Match(rules, "csa/csa-ccm-v4.1.0.xlsx")
	if !m.matched {
		t.Fatal("expected match")
	}
	if deref(m.frameworkCode) != "csaccm" {
		t.Errorf("got %q, want csaccm", deref(m.frameworkCode))
	}
}

func TestMatch_QualifierPreserved(t *testing.T) {
	rules := []Rule{
		{Ordinal: 100, Pattern: "csa/csa-caiq-v4.1.0.xlsx", FrameworkCode: strPtr("csaccm"), VersionLabel: strPtr("v4.1"), DocRole: strPtr("companion-workbook"), Qualifier: "caiq", FileFormat: strPtr("xlsx")},
	}
	m := Match(rules, "csa/csa-caiq-v4.1.0.xlsx")
	if !m.matched {
		t.Fatal("expected match")
	}
	if m.qualifier != "caiq" {
		t.Errorf("got qualifier %q, want 'caiq'", m.qualifier)
	}
}

// --- Scanner tests ---

// fakeQuerier implements dbingest.Querier for scanner unit tests.
type fakeQuerier struct {
	rows      map[string]dbingest.IngestManifestFile
	nextID    int64
	demoted   int64
	errors    map[int64]string
	lastPaths []string
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		rows:   map[string]dbingest.IngestManifestFile{},
		nextID: 1,
		errors: map[int64]string{},
	}
}

func (f *fakeQuerier) UpsertManifestFile(_ context.Context, arg dbingest.UpsertManifestFileParams) (dbingest.IngestManifestFile, error) {
	existing, exists := f.rows[arg.RelPath]
	if exists {
		// If sha256 changed, reset stages.
		if existing.Sha256 != arg.Sha256 {
			existing.ExtractedAt = nil
			existing.NormalizedAt = nil
			existing.IndexedAt = nil
			existing.StageError = ""
		}
		existing.Sha256 = arg.Sha256
		existing.SizeBytes = arg.SizeBytes
		existing.FrameworkCode = arg.FrameworkCode
		existing.VersionLabel = arg.VersionLabel
		existing.DocRole = arg.DocRole
		existing.Qualifier = arg.Qualifier
		existing.FileFormat = arg.FileFormat
		existing.Status = "active"
		existing.Ignored = arg.Ignored
		existing.IgnoreReason = arg.IgnoreReason
		f.rows[arg.RelPath] = existing
		return existing, nil
	}
	row := dbingest.IngestManifestFile{
		ID:            f.nextID,
		RelPath:       arg.RelPath,
		Sha256:        arg.Sha256,
		SizeBytes:     arg.SizeBytes,
		FrameworkCode: arg.FrameworkCode,
		VersionLabel:  arg.VersionLabel,
		DocRole:       arg.DocRole,
		Qualifier:     arg.Qualifier,
		FileFormat:    arg.FileFormat,
		Status:        "active",
		Ignored:       arg.Ignored,
		IgnoreReason:  arg.IgnoreReason,
	}
	f.nextID++
	f.rows[arg.RelPath] = row
	return row, nil
}

func (f *fakeQuerier) DemoteMissingManifestFiles(_ context.Context, paths []string) (int64, error) {
	f.lastPaths = paths
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	var count int64
	for k, r := range f.rows {
		if r.Status == "active" && !seen[k] {
			r.Status = "removed"
			f.rows[k] = r
			count++
		}
	}
	f.demoted = count
	return count, nil
}

func (f *fakeQuerier) SetStageError(_ context.Context, arg dbingest.SetStageErrorParams) error {
	f.errors[arg.ID] = arg.StageError
	return nil
}

// Stub out the rest of the Querier interface.
func (f *fakeQuerier) ListActiveManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) ListFilesToExtract(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) ListFilesToIndex(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) ListFilesToNormalize(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) ListIgnoredManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) ListUnrecognizedManifestFiles(_ context.Context) ([]dbingest.IngestManifestFile, error) {
	return nil, nil
}
func (f *fakeQuerier) MarkExtracted(_ context.Context, _ int64) error  { return nil }
func (f *fakeQuerier) MarkIndexed(_ context.Context, _ int64) error    { return nil }
func (f *fakeQuerier) MarkNormalized(_ context.Context, _ int64) error { return nil }

func makeTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	return d
}

func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	p := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func TestScan_MatchAndIgnore(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "nist/catalog.json", `{"test": true}`)
	writeFile(t, dir, "README.md", "# corpus")

	rules := []Rule{
		{Ordinal: 100, Pattern: "nist/catalog.json", FrameworkCode: strPtr("nist80053"), VersionLabel: strPtr("r5"), DocRole: strPtr("main"), FileFormat: strPtr("oscal-json")},
		{Ordinal: 200, Pattern: "README.md", Ignore: true, IgnoreReason: "readme"},
	}

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: rules, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Total != 2 {
		t.Errorf("total=%d, want 2", sum.Total)
	}
	if sum.Matched != 1 {
		t.Errorf("matched=%d, want 1", sum.Matched)
	}
	if sum.Ignored != 1 {
		t.Errorf("ignored=%d, want 1", sum.Ignored)
	}
	if sum.Unrecognized != 0 {
		t.Errorf("unrecognized=%d, want 0", sum.Unrecognized)
	}

	row := fq.rows["nist/catalog.json"]
	if deref(row.FrameworkCode) != "nist80053" {
		t.Errorf("framework=%q, want nist80053", deref(row.FrameworkCode))
	}
	if row.Sha256 != fileSHA256(`{"test": true}`) {
		t.Error("sha256 mismatch")
	}

	readme := fq.rows["README.md"]
	if !readme.Ignored {
		t.Error("README.md should be ignored")
	}
}

func TestScan_Unrecognized(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "unknown/file.txt", "data")

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: nil, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Unrecognized != 1 {
		t.Errorf("unrecognized=%d, want 1", sum.Unrecognized)
	}
}

func TestScan_Demote(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "a.txt", "a")

	fq := newFakeQuerier()
	// Pre-populate a row that will be missing.
	fq.rows["old.txt"] = dbingest.IngestManifestFile{
		ID: 99, RelPath: "old.txt", Status: "active",
	}

	scanner := &Scanner{DataDir: dir, Rules: nil, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Demoted != 1 {
		t.Errorf("demoted=%d, want 1", sum.Demoted)
	}
	if fq.rows["old.txt"].Status != "removed" {
		t.Error("old.txt should be demoted to removed")
	}
}

func TestScan_SHA256ChangeResetsStages(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "doc.json", "v2-content")

	fq := newFakeQuerier()
	// Simulate a previously-extracted file with different sha256.
	fq.rows["doc.json"] = dbingest.IngestManifestFile{
		ID: 1, RelPath: "doc.json", Sha256: "old-hash", Status: "active",
		ExtractedAt: func() *time.Time { t := time.Now(); return &t }(),
	}

	rules := []Rule{
		{Ordinal: 100, Pattern: "doc.json", FrameworkCode: strPtr("test"), VersionLabel: strPtr("v1"), DocRole: strPtr("main"), FileFormat: strPtr("oscal-json")},
	}

	scanner := &Scanner{DataDir: dir, Rules: rules, Log: testLogger()}
	_, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	row := fq.rows["doc.json"]
	if row.ExtractedAt != nil {
		t.Error("expected extracted_at reset on sha256 change")
	}
}

func TestScan_AmbiguityDetection(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "nist/a.json", "content-a")
	writeFile(t, dir, "nist/b.json", "content-b")

	// Both files match same (framework, version, role, qualifier, format).
	rules := []Rule{
		{Ordinal: 100, Pattern: "nist/a.json", FrameworkCode: strPtr("nist80053"), VersionLabel: strPtr("r5"), DocRole: strPtr("main"), Qualifier: "", FileFormat: strPtr("oscal-json")},
		{Ordinal: 200, Pattern: "nist/b.json", FrameworkCode: strPtr("nist80053"), VersionLabel: strPtr("r5"), DocRole: strPtr("main"), Qualifier: "", FileFormat: strPtr("oscal-json")},
	}

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: rules, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Ambiguous != 2 {
		t.Errorf("ambiguous=%d, want 2", sum.Ambiguous)
	}
	// Both should have stage_error set.
	if len(fq.errors) != 2 {
		t.Errorf("errors=%d, want 2", len(fq.errors))
	}
}

func TestScan_GuideNotAmbiguous(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "csa/guide1.pdf", "g1")
	writeFile(t, dir, "csa/guide2.pdf", "g2")

	// Two guides with same (framework, version, role, qualifier, format) — not ambiguous.
	rules := []Rule{
		{Ordinal: 100, Pattern: "csa/guide1.pdf", FrameworkCode: strPtr("csaccm"), VersionLabel: strPtr("v4.1"), DocRole: strPtr("guide"), Qualifier: "intro", FileFormat: strPtr("pdf")},
		{Ordinal: 200, Pattern: "csa/guide2.pdf", FrameworkCode: strPtr("csaccm"), VersionLabel: strPtr("v4.1"), DocRole: strPtr("guide"), Qualifier: "intro", FileFormat: strPtr("pdf")},
	}

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: rules, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Ambiguous != 0 {
		t.Errorf("ambiguous=%d, want 0 (guides are exempt)", sum.Ambiguous)
	}
}

func TestScan_SkipsGitDir(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, ".git/config", "gitconfig")

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: nil, Log: testLogger()}
	sum, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if sum.Total != 1 {
		t.Errorf("total=%d, want 1 (.git should be skipped)", sum.Total)
	}
}

func TestScan_Idempotent(t *testing.T) {
	dir := makeTempDir(t)
	writeFile(t, dir, "doc.json", "content")

	rules := []Rule{
		{Ordinal: 100, Pattern: "doc.json", FrameworkCode: strPtr("test"), VersionLabel: strPtr("v1"), DocRole: strPtr("main"), FileFormat: strPtr("oscal-json")},
	}

	fq := newFakeQuerier()
	scanner := &Scanner{DataDir: dir, Rules: rules, Log: testLogger()}

	sum1, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	sum2, err := scanner.Scan(context.Background(), fq)
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if sum1.Matched != sum2.Matched || sum1.Total != sum2.Total {
		t.Errorf("scan not idempotent: %+v vs %+v", sum1, sum2)
	}
}

// testLogger returns a no-op logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
