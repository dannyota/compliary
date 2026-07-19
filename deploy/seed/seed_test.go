package seed

import (
	"encoding/csv"
	"strconv"
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
		"annex-control",
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
