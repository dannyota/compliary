package normalize

import (
	"encoding/json"
	"testing"
)

// syntheticOLIR mirrors the OLIR crosswalk capture: per-family sheets, header
// row, focal (col A) + reference (col D) pairs, a Definitions sheet to skip,
// and noise rows the citation gates must reject.
const syntheticOLIR = `{
  "sheets": [
    {"name": "Relationships-AC", "rows": [
      {"ref": "A1", "value": "Focal Document\nElement"},
      {"ref": "D1", "value": "Reference Document Element"},
      {"ref": "A2", "value": "AC-01"}, {"ref": "B2", "value": "long verbatim text"}, {"ref": "D2", "value": "5.2"},
      {"ref": "A3", "value": "AC-01"}, {"ref": "D3", "value": "A.5.15"},
      {"ref": "A4", "value": "AC-02(01)"}, {"ref": "D4", "value": "A.8.2"},
      {"ref": "A5", "value": "AC-01"}, {"ref": "D5", "value": "5.2"},
      {"ref": "A6", "value": "not a citation"}, {"ref": "D6", "value": "A.5.1"},
      {"ref": "A7", "value": "AC-03"}, {"ref": "D7", "value": "see comment"}
    ]},
    {"name": "Definitions", "rows": [
      {"ref": "A1", "value": "Relationship"}, {"ref": "D1", "value": "5.2"}
    ]}
  ]
}`

func TestParseOLIRPairs(t *testing.T) {
	pairs, err := parseOLIRPairs(json.RawMessage(syntheticOLIR))
	if err != nil {
		t.Fatalf("parseOLIRPairs: %v", err)
	}
	// Expected: AC-01→5.2 (row 5 dedupes), AC-01→A.5.15, AC-02(01)→A.8.2.
	// Rejected: header, non-citation focal, prose reference, Definitions sheet.
	if len(pairs) != 3 {
		t.Fatalf("pairs=%d, want 3; got %+v", len(pairs), pairs)
	}
	got := map[string]string{}
	for _, p := range pairs {
		got[p.FocalNorm] = got[p.FocalNorm] + "|" + p.RefNorm
		if p.Provenance == "" || p.Provenance[:5] != "sheet" {
			t.Errorf("provenance should be sheet+row ref, got %q", p.Provenance)
		}
	}
	if got["AC-02(01)"] != "|A.8.2" {
		t.Errorf("enhancement pair wrong: %q", got["AC-02(01)"])
	}
	found52, foundA515 := false, false
	for _, p := range pairs {
		if p.FocalNorm == "AC-01" && p.RefNorm == "5.2" {
			found52 = true
		}
		if p.FocalNorm == "AC-01" && p.RefNorm == "A.5.15" {
			foundA515 = true
		}
	}
	if !found52 || !foundA515 {
		t.Errorf("AC-01 pairs missing: 5.2=%v A.5.15=%v", found52, foundA515)
	}
}

func TestParseOLIRPairs_RealShape(t *testing.T) {
	// Real-file gate check: zero-padded focals with enhancements, both ISO
	// citation shapes accepted, nothing else.
	ok := []struct{ f, r string }{{"SC-07", "A.8.20"}, {"PM-01", "4.4"}, {"IA-02(01)", "A.5.17"}}
	bad := []struct{ f, r string }{{"AC-1", "5.2"}, {"AC-01", "ISO 5.2"}, {"Withdrawn", "A.5.1"}}
	for _, c := range ok {
		if !reOLIRFocal.MatchString(c.f) || !reOLIRISORef.MatchString(c.r) {
			t.Errorf("should accept %s→%s", c.f, c.r)
		}
	}
	for _, c := range bad {
		if reOLIRFocal.MatchString(c.f) && reOLIRISORef.MatchString(c.r) {
			t.Errorf("should reject %s→%s", c.f, c.r)
		}
	}
}
