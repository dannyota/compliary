package seed

import (
	"bufio"
	"encoding/csv"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readCSV(t *testing.T, name string) [][]string {
	t.Helper()
	f, err := FS.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	if len(recs) == 0 {
		t.Fatalf("%s: empty", name)
	}
	return recs
}

func TestFrameworkSeed(t *testing.T) {
	recs := readCSV(t, "framework.csv")
	rows := recs[1:]
	if len(rows) != 15 {
		t.Fatalf("framework.csv: want 15 frameworks, got %d", len(rows))
	}

	access := map[string]bool{"auto-fetch": true, "form-gated": true, "byo": true}
	license := map[string]bool{"public-domain": true, "open-restricted": true, "licensed": true}
	serve := map[string]bool{"full": true, "auth-text-only": true, "operator-assumes-risk": true}

	seen := map[string]bool{}
	for _, r := range rows {
		code := r[0]
		if seen[code] {
			t.Errorf("framework %q: duplicate code", code)
		}
		seen[code] = true
		if !access[r[3]] {
			t.Errorf("framework %q: bad source_access %q", code, r[3])
		}
		if !license[r[4]] {
			t.Errorf("framework %q: bad license_class %q", code, r[4])
		}
		if _, err := strconv.ParseBool(r[5]); err != nil {
			t.Errorf("framework %q: bad ingest_enabled %q", code, r[5])
		}
		if !serve[r[6]] {
			t.Errorf("framework %q: bad serve_policy %q", code, r[6])
		}
		if r[7] == "" {
			t.Errorf("framework %q: empty citation_scheme", code)
		}
		// Licensed text must never be servable without auth by default.
		if r[4] == "licensed" && r[6] == "full" {
			t.Errorf("framework %q: licensed but serve_policy 'full'", code)
		}
	}
}

func TestFrameworkVersionSeed(t *testing.T) {
	frameworks := map[string]bool{}
	for _, r := range readCSV(t, "framework.csv")[1:] {
		frameworks[r[0]] = true
	}

	rows := readCSV(t, "framework_version.csv")[1:]
	current := map[string]int{}
	seen := map[string]bool{}
	for _, r := range rows {
		fw, label := r[0], r[1]
		if !frameworks[fw] {
			t.Errorf("version %s/%s: unknown framework_code", fw, label)
		}
		key := fw + "|" + label
		if seen[key] {
			t.Errorf("version %s: duplicate", key)
		}
		seen[key] = true
		if r[2] != "" {
			if _, err := time.Parse("2006-01-02", r[2]); err != nil {
				t.Errorf("version %s: bad published_on %q", key, r[2])
			}
		}
		isCurrent, err := strconv.ParseBool(r[3])
		if err != nil {
			t.Errorf("version %s: bad is_current %q", key, r[3])
		}
		if isCurrent {
			current[fw]++
		}
	}
	// Every framework has exactly one current version.
	for fw := range frameworks {
		if current[fw] != 1 {
			t.Errorf("framework %q: %d current versions, want exactly 1", fw, current[fw])
		}
	}
}

func TestControlKindSeed(t *testing.T) {
	want := []string{
		"domain", "family", "clause", "control", "enhancement", "criterion",
		"point-of-focus", "requirement", "objective", "practice", "safeguard",
		"annex-control", "function", "category", "subcategory",
	}
	rows := readCSV(t, "control_kind.csv")[1:]
	got := map[string]bool{}
	for _, r := range rows {
		got[r[0]] = true
	}
	if len(rows) != len(want) {
		t.Errorf("control_kind.csv: %d kinds, want %d", len(rows), len(want))
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("control_kind.csv: missing kind %q", k)
		}
	}
}

func TestMappingSourceSeed(t *testing.T) {
	rows := readCSV(t, "mapping_source.csv")[1:]
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r[0]] {
			t.Errorf("mapping_source %q: duplicate", r[0])
		}
		seen[r[0]] = true
	}
	for _, required := range []string{"publisher-catalog", "operator"} {
		if !seen[required] {
			t.Errorf("mapping_source.csv: missing %q", required)
		}
	}
}

func TestReferenceSourceSeed(t *testing.T) {
	// Load frameworks + versions for cross-reference.
	frameworks := map[string]bool{}
	for _, r := range readCSV(t, "framework.csv")[1:] {
		frameworks[r[0]] = true
	}
	versions := map[string]bool{}
	for _, r := range readCSV(t, "framework_version.csv")[1:] {
		versions[r[0]+"|"+r[1]] = true
	}

	rows := readCSV(t, "reference_source.csv")[1:]
	if len(rows) != 8 {
		t.Fatalf("reference_source.csv: want 8 rows, got %d", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		prefix := r[0]
		if seen[prefix] {
			t.Errorf("reference_source %q: duplicate prefix", prefix)
		}
		seen[prefix] = true
		if !frameworks[r[1]] {
			t.Errorf("reference_source %q: unknown framework_code %q", prefix, r[1])
		}
		// to_version_label may be empty (NULL) for version-unspecified edges.
		if r[2] != "" {
			if !versions[r[1]+"|"+r[2]] {
				t.Errorf("reference_source %q: unknown version %s/%s", prefix, r[1], r[2])
			}
		}
		if _, err := strconv.ParseBool(r[4]); err != nil {
			t.Errorf("reference_source %q: bad enabled %q", prefix, r[4])
		}
	}
}

func TestControlTitleSeed(t *testing.T) {
	// Load frameworks + versions for cross-reference.
	frameworks := map[string]bool{}
	for _, r := range readCSV(t, "framework.csv")[1:] {
		frameworks[r[0]] = true
	}
	versions := map[string]bool{}
	for _, r := range readCSV(t, "framework_version.csv")[1:] {
		versions[r[0]+"|"+r[1]] = true
	}

	rows := readCSV(t, "control_title.csv")[1:]
	if len(rows) < 700 {
		t.Fatalf("control_title.csv: want >=700 rows, got %d", len(rows))
	}

	seen := map[string]bool{}
	for _, r := range rows {
		fw, ver, cite, title := r[0], r[1], r[2], r[3]
		key := fw + "|" + ver + "|" + cite
		if seen[key] {
			t.Errorf("control_title %s: duplicate", key)
		}
		seen[key] = true
		if !frameworks[fw] {
			t.Errorf("control_title %s: unknown framework_code %q", key, fw)
		}
		if !versions[fw+"|"+ver] {
			t.Errorf("control_title %s: unknown framework_version", key)
		}
		if title == "" {
			t.Errorf("control_title %s: empty title", key)
		}
		if cite == "" {
			t.Errorf("control_title %s: empty citation_norm", key)
		}
	}
}

// TestControlTitleCorpusCoverage validates that every curated title targets
// a citation that exists in the corpus. The corpus-citations snapshot is
// deploy/eval/corpus-citations.txt (framework|version|citation per line).
// Verbatim verification was done upstream during title authoring —
// no spot-check needed here (noted per task spec).
func TestControlTitleCorpusCoverage(t *testing.T) {
	const snapshotName = "corpus-citations.txt"
	// The snapshot lives in deploy/eval/, not deploy/seed/. Open via os.
	f, err := os.Open("../eval/" + snapshotName)
	if err != nil {
		t.Skipf("corpus-citations.txt not found: %v", err)
	}
	defer func() { _ = f.Close() }()

	corpus := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			corpus[strings.ToLower(line)] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read corpus-citations.txt: %v", err)
	}
	if len(corpus) == 0 {
		t.Fatal("corpus-citations.txt is empty")
	}

	rows := readCSV(t, "control_title.csv")[1:]
	var missing int
	for _, r := range rows {
		key := strings.ToLower(r[0] + "|" + r[1] + "|" + r[2])
		if !corpus[key] {
			t.Errorf("control_title %s|%s|%s: not in corpus snapshot", r[0], r[1], r[2])
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d control_title citations missing from corpus snapshot", missing)
	}
}

func TestFileRuleSeed(t *testing.T) {
	// Load frameworks for cross-reference.
	frameworks := map[string]bool{}
	for _, r := range readCSV(t, "framework.csv")[1:] {
		frameworks[r[0]] = true
	}

	// Load framework versions for cross-reference.
	versions := map[string]bool{}
	for _, r := range readCSV(t, "framework_version.csv")[1:] {
		versions[r[0]+"|"+r[1]] = true
	}

	roles := map[string]bool{
		"main": true, "amendment": true, "companion-workbook": true,
		"changelog": true, "guide": true,
	}
	formats := map[string]bool{"oscal-json": true, "xlsx": true, "pdf": true}
	licenseKinds := map[string]bool{
		"public-domain": true, "cc-by-nc-nd": true, "click-through": true,
		"purchased": true, "unverified": true,
	}

	rows := readCSV(t, "file_rule.csv")[1:]
	if len(rows) != 30 {
		t.Fatalf("file_rule.csv: want 30 rules, got %d", len(rows))
	}

	seenPattern := map[string]bool{}
	seenOrdinal := map[int]bool{}
	for _, r := range rows {
		pattern := r[1]
		ordinal, err := strconv.Atoi(r[0])
		if err != nil {
			t.Errorf("rule %q: bad ordinal %q", pattern, r[0])
			continue
		}
		if seenOrdinal[ordinal] {
			t.Errorf("rule %q: duplicate ordinal %d", pattern, ordinal)
		}
		seenOrdinal[ordinal] = true
		if seenPattern[pattern] {
			t.Errorf("rule %q: duplicate pattern", pattern)
		}
		seenPattern[pattern] = true

		// Pattern must compile under path.Match.
		if _, err := path.Match(pattern, pattern); err != nil {
			t.Errorf("rule %q: bad pattern: %v", pattern, err)
		}

		ignore, err := strconv.ParseBool(r[7])
		if err != nil {
			t.Errorf("rule %q: bad ignore %q", pattern, r[7])
			continue
		}

		if ignore {
			// Ignore rules must have empty framework fields.
			if r[2] != "" || r[3] != "" || r[4] != "" || r[6] != "" {
				t.Errorf("rule %q: ignore rule has framework fields set", pattern)
			}
			if r[8] == "" {
				t.Errorf("rule %q: ignore rule has empty ignore_reason", pattern)
			}
		} else {
			// Match rules must have all framework fields set.
			if r[2] == "" || r[3] == "" || r[4] == "" || r[6] == "" {
				t.Errorf("rule %q: match rule has empty framework fields", pattern)
			}
			if !frameworks[r[2]] {
				t.Errorf("rule %q: unknown framework_code %q", pattern, r[2])
			}
			if !versions[r[2]+"|"+r[3]] {
				t.Errorf("rule %q: unknown framework_version %s/%s", pattern, r[2], r[3])
			}
			if !roles[r[4]] {
				t.Errorf("rule %q: bad doc_role %q", pattern, r[4])
			}
			if !formats[r[6]] {
				t.Errorf("rule %q: bad file_format %q", pattern, r[6])
			}
			if r[9] != "" && !licenseKinds[r[9]] {
				t.Errorf("rule %q: bad license_kind %q", pattern, r[9])
			}
		}
	}
}

// TestFileRuleClassification verifies that every rel_path from the expected
// classification table matches exactly the expected rule pattern. This is
// metadata only — no licensed document text.
func TestFileRuleClassification(t *testing.T) {
	type expected struct {
		relPath       string
		pattern       string
		frameworkCode string
		versionLabel  string
		docRole       string
		qualifier     string
		fileFormat    string
		ignore        bool
	}
	cases := []expected{
		{"nist/nist-sp-800-53r5-oscal-catalog.json", "nist/nist-sp-800-53r5-oscal-catalog.json", "nist80053", "r5", "main", "", "oscal-json", false},
		{"nist/nist-sp-800-53r5.pdf", "nist/nist-sp-800-53r5.pdf", "nist80053", "r5", "guide", "pdf-rendering", "pdf", false},
		{"nist/nist-csf-2.0.xlsx", "nist/nist-csf-2.0.xlsx", "nistcsf", "2.0", "main", "", "xlsx", false},
		{"nist/nist-csf-2.0.pdf", "nist/nist-csf-2.0.pdf", "nistcsf", "2.0", "guide", "pdf-rendering", "pdf", false},
		{"cis/cis-controls-version-8.1.2-march-2025.xlsx", "cis/cis-controls-version-8.1.2-march-2025.xlsx", "ciscontrols", "v8.1", "main", "", "xlsx", false},
		{"cis/cis-controls-version-8.1.2-change-log-march-2025.xlsx", "cis/cis-controls-version-8.1.2-change-log-march-2025.xlsx", "ciscontrols", "v8.1", "changelog", "", "xlsx", false},
		{"cis/cis-controls-guide-v8.1.2-0325-v2.pdf", "cis/cis-controls-guide-v8.1.2-0325-v2.pdf", "ciscontrols", "v8.1", "guide", "", "pdf", false},
		{"pcissc/pci-dss-v4.0.1.pdf", "pcissc/pci-dss-v4.0.1.pdf", "pcidss", "v4.0.1", "main", "", "pdf", false},
		{"aicpa/aicpa-tsc-2017-points-of-focus-2022.pdf", "aicpa/aicpa-tsc-2017-points-of-focus-2022.pdf", "soc2tsc", "2017", "main", "", "pdf", false},
		{"csa/csa-ccm-v4.1.0.xlsx", "csa/csa-ccm-v4.1.0.xlsx", "csaccm", "v4.1", "main", "", "xlsx", false},
		{"csa/csa-caiq-v4.1.0.xlsx", "csa/csa-caiq-v4.1.0.xlsx", "csaccm", "v4.1", "companion-workbook", "caiq", "xlsx", false},
		{"csa/csa-caiq-v4.0.3-to-v4.1-changes.xlsx", "csa/csa-caiq-v4.0.3-to-v4.1-changes.xlsx", "csaccm", "v4.1", "changelog", "caiq", "xlsx", false},
		{"csa/csa-ccm-caiq-guide.pdf", "csa/csa-ccm-caiq-guide.pdf", "csaccm", "v4.1", "guide", "caiq-guide", "pdf", false},
		{"csa/csa-ccm-introductory-guidance.pdf", "csa/csa-ccm-introductory-guidance.pdf", "csaccm", "v4.1", "guide", "introductory-guidance", "pdf", false},
		{"csa/csa-ccm-v4.1-implementation-guidelines.pdf", "csa/csa-ccm-v4.1-implementation-guidelines.pdf", "csaccm", "v4.1", "guide", "implementation-guidelines", "pdf", false},
		{"csa/csa-continuous-audit-metrics-catalog-v1.1.pdf", "csa/csa-continuous-audit-metrics-catalog-v1.1.pdf", "", "", "", "", "", true},
		{"csa/csa-key-metrics-code-of-practice.pdf", "csa/csa-key-metrics-code-of-practice.pdf", "", "", "", "", "", true},
		{"isaca/cobit-2019-framework-governance-and-management-objectives.pdf", "isaca/cobit-2019-framework-governance-and-management-objectives.pdf", "cobit", "2019", "main", "", "pdf", false},
		{"isaca/cobit-2019-framework-introduction-and-methodology.pdf", "isaca/cobit-2019-framework-introduction-and-methodology.pdf", "cobit", "2019", "guide", "introduction-and-methodology", "pdf", false},
		{"iso/iso-iec-27001-2022.pdf", "iso/iso-iec-27001-2022.pdf", "iso27001", "2022", "main", "", "pdf", false},
		{"iso/iso-iec-27001-2022-amd1-2024.pdf", "iso/iso-iec-27001-2022-amd1-2024.pdf", "iso27001", "2022", "amendment", "amd1-2024", "pdf", false},
		{"iso/iso-22301-2019-amd1-2024.pdf", "iso/iso-22301-2019-amd1-2024.pdf", "iso22301", "2019", "amendment", "amd1-2024", "pdf", false},
		{"iso/iso-iec-27002-2022.pdf", "iso/iso-iec-27002-2022.pdf", "iso27002", "2022", "main", "", "pdf", false},
		{"iso/iso-iec-27017-2015.pdf", "iso/iso-iec-27017-2015.pdf", "iso27017", "2015", "main", "", "pdf", false},
		{"iso/iso-iec-27018-2019.pdf", "iso/iso-iec-27018-2019.pdf", "iso27018", "2019", "main", "", "pdf", false},
		{"README.md", "README.md", "", "", "", "", "", true},
	}

	rows := readCSV(t, "file_rule.csv")[1:]
	type rule struct {
		pattern       string
		frameworkCode string
		versionLabel  string
		docRole       string
		qualifier     string
		fileFormat    string
		ignore        bool
	}

	// Build rules ordered by ordinal for matching.
	type ordRule struct {
		ordinal int
		rule
	}
	var rules []ordRule
	for _, r := range rows {
		ord, _ := strconv.Atoi(r[0])
		ign, _ := strconv.ParseBool(r[7])
		rules = append(rules, ordRule{
			ordinal: ord,
			rule: rule{
				pattern:       r[1],
				frameworkCode: r[2],
				versionLabel:  r[3],
				docRole:       r[4],
				qualifier:     r[5],
				fileFormat:    r[6],
				ignore:        ign,
			},
		})
	}

	for _, tc := range cases {
		var matched *ordRule
		for i := range rules {
			ok, err := path.Match(rules[i].pattern, tc.relPath)
			if err != nil {
				t.Fatalf("pattern %q: %v", rules[i].pattern, err)
			}
			if ok {
				matched = &rules[i]
				break
			}
		}
		if matched == nil {
			t.Errorf("rel_path %q: no matching rule", tc.relPath)
			continue
		}
		if matched.pattern != tc.pattern {
			t.Errorf("rel_path %q: matched pattern %q, want %q", tc.relPath, matched.pattern, tc.pattern)
		}
		if matched.frameworkCode != tc.frameworkCode {
			t.Errorf("rel_path %q: framework_code %q, want %q", tc.relPath, matched.frameworkCode, tc.frameworkCode)
		}
		if matched.versionLabel != tc.versionLabel {
			t.Errorf("rel_path %q: version_label %q, want %q", tc.relPath, matched.versionLabel, tc.versionLabel)
		}
		if matched.docRole != tc.docRole {
			t.Errorf("rel_path %q: doc_role %q, want %q", tc.relPath, matched.docRole, tc.docRole)
		}
		if matched.qualifier != tc.qualifier {
			t.Errorf("rel_path %q: qualifier %q, want %q", tc.relPath, matched.qualifier, tc.qualifier)
		}
		if matched.fileFormat != tc.fileFormat {
			t.Errorf("rel_path %q: file_format %q, want %q", tc.relPath, matched.fileFormat, tc.fileFormat)
		}
		if matched.ignore != tc.ignore {
			t.Errorf("rel_path %q: ignore %v, want %v", tc.relPath, matched.ignore, tc.ignore)
		}
	}
}
