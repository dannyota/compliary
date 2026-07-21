package normalize

import (
	"encoding/json"
	"testing"
)

// syntheticCISMap mirrors the CIS mapping workbook: definition rows without
// relationship/target, mapping rows with float-mangled safeguard numbers, a
// header, and rows the gates must skip.
const syntheticCISMap = `{
  "sheets": [
    {"name": "All CIS Controls & Safeguards", "rows": [
      {"ref": "B1", "value": "CIS Control"}, {"ref": "K1", "value": "Relationship"}, {"ref": "L1", "value": "Control #"},
      {"ref": "B2", "value": "4"}, {"ref": "F2", "value": "Secure Configuration of Enterprise Assets and Software"},
      {"ref": "C4", "value": "4.0999999999999996"}, {"ref": "F4", "value": "Establish and Maintain a Secure Configuration Process"}, {"ref": "K4", "value": "Subset"}, {"ref": "L4", "value": "A8.9"},
      {"ref": "C5", "value": "4.0999999999999996"}, {"ref": "F5", "value": "Enforce Automatic Device Lockout on Portable End-User Devices"}, {"ref": "K5", "value": "Equivalent"}, {"ref": "L5", "value": "A8.1"},
      {"ref": "C6", "value": "4.2"}, {"ref": "F6", "value": "Establish and Maintain a Secure Configuration Process for Network Infrastructure"}, {"ref": "K6", "value": "Superset"}, {"ref": "L6", "value": "7.5.1"},
      {"ref": "C7", "value": "4.3"}, {"ref": "F7", "value": "Some Safeguard"}, {"ref": "K7", "value": "Related"}, {"ref": "L7", "value": "A8.2"},
      {"ref": "C8", "value": "4.4"}, {"ref": "F8", "value": "Another Safeguard"}, {"ref": "K8", "value": "Subset"}, {"ref": "L8", "value": "see note"},
      {"ref": "C9", "value": "8.11"}, {"ref": "F9", "value": "Conduct Audit Log Reviews"}, {"ref": "L9", "value": "A8.15"}
    ]},
    {"name": "Unmapped CIS", "rows": [
      {"ref": "K1", "value": "Subset"}, {"ref": "L1", "value": "A9.9"}, {"ref": "F1", "value": "Should not be parsed"}
    ]}
  ]
}`

func TestParseCISMappingPairs(t *testing.T) {
	pairs, skipped, err := parseCISMappingPairs(json.RawMessage(syntheticCISMap), normalizeCISISOTarget)
	if err != nil {
		t.Fatalf("parseCISMappingPairs: %v", err)
	}
	// Rows 4,5,6 parse; row 9 (blank relationship, asserted target) parses as
	// 'related'; row 7 (unknown relationship "Related") and row 8 (unparseable
	// target) are skipped; the Unmapped sheet is ignored.
	if len(pairs) != 4 {
		t.Fatalf("pairs=%d, want 4; got %+v", len(pairs), pairs)
	}
	if skipped != 2 {
		t.Errorf("skipped=%d, want 2", skipped)
	}
	byTitle := map[string]cisMapPair{}
	for _, p := range pairs {
		byTitle[p.SafeguardTitle] = p
	}
	// Float-merged 4.1 vs 4.10 disambiguate by title.
	p1 := byTitle["Establish and Maintain a Secure Configuration Process"]
	if p1.TargetNorm != "A.8.9" || p1.Relationship != "subset-of" {
		t.Errorf("4.1 row: %+v", p1)
	}
	p2 := byTitle["Enforce Automatic Device Lockout on Portable End-User Devices"]
	if p2.TargetNorm != "A.8.1" || p2.Relationship != "equivalent" {
		t.Errorf("4.10 row: %+v", p2)
	}
	p3 := byTitle["Establish and Maintain a Secure Configuration Process for Network Infrastructure"]
	if p3.TargetNorm != "7.5.1" || p3.Relationship != "superset-of" {
		t.Errorf("clause row: %+v", p3)
	}
	// Blank relationship with an asserted target → 'related', never guessed stronger.
	p4 := byTitle["Conduct Audit Log Reviews"]
	if p4.TargetNorm != "A.8.15" || p4.Relationship != "related" {
		t.Errorf("blank-relationship row: %+v", p4)
	}
}

func TestCISTargetNormalizers(t *testing.T) {
	cases := []struct {
		fn   func(string) (string, bool)
		in   string
		want string
		ok   bool
	}{
		{normalizeCISISOTarget, "A5.9", "A.5.9", true},
		{normalizeCISISOTarget, "A8.28", "A.8.28", true},
		{normalizeCISISOTarget, "7.5.1", "7.5.1", true},
		{normalizeCISISOTarget, "see comment", "", false},
		{normalizeCISCSFTarget, "ID.AM-01", "ID.AM-01", true},
		{normalizeCISCSFTarget, "pr.ps-03", "PR.PS-03", true},
		{normalizeCISCSFTarget, "GOVERN", "", false},
		{normalizeCIS80053Target, "CM-8", "CM-08", true},
		{normalizeCIS80053Target, "CM-8(1)", "CM-08(01)", true},
		{normalizeCIS80053Target, "AC-17(21)", "AC-17(21)", true},
		{normalizeCIS80053Target, "N/A", "", false},
	}
	for _, c := range cases {
		got, ok := c.fn(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normalize(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
