package normalize

import (
	"context"
	"testing"

	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// inventedCuratedRows are fixture curated edges — synthetic frameworks and
// citations, not the real seed — exercising the matching logic in isolation.
var inventedCuratedRows = []CuratedEdge{
	// from resolves; version-pinned target.
	{FromFramework: "fwa", FromVersion: "1", FromCitation: "5.2", ToFramework: "fwb", ToVersion: "v8", ToCitation: "1", Relationship: "related", MappingSource: "operator", Note: "n1"},
	// from resolves; case-insensitive citation match (pe-03 vs index PE-03).
	{FromFramework: "fwa", FromVersion: "1", FromCitation: "pe-03", ToFramework: "fwb", ToVersion: "v8", ToCitation: "2", Relationship: "intersects", MappingSource: "operator", Note: "n2"},
	// from resolves; unpinned target version → nil ToVersionLabel.
	{FromFramework: "fwa", FromVersion: "1", FromCitation: "5.3", ToFramework: "fwb", ToVersion: "", ToCitation: "3", Relationship: "related", MappingSource: "operator", Note: "n3"},
	// from NOT in index → unresolved.
	{FromFramework: "fwa", FromVersion: "1", FromCitation: "9.9", ToFramework: "fwb", ToVersion: "v8", ToCitation: "4", Relationship: "related", MappingSource: "operator", Note: "n4"},
	// wrong version → unresolved (version is part of the key).
	{FromFramework: "fwa", FromVersion: "2", FromCitation: "5.2", ToFramework: "fwb", ToVersion: "v8", ToCitation: "5", Relationship: "related", MappingSource: "operator", Note: "n5"},
}

var inventedFromIndex = map[string]int64{
	fromKey("fwa", "1", "5.2"):   101,
	fromKey("fwa", "1", "PE-03"): 102,
	fromKey("fwa", "1", "5.3"):   103,
}

func TestBuildCuratedUpserts(t *testing.T) {
	params, unresolved := buildCuratedUpserts(inventedCuratedRows, inventedFromIndex)

	if len(params) != 3 {
		t.Fatalf("emitted params = %d, want 3", len(params))
	}
	if len(unresolved) != 2 {
		t.Fatalf("unresolved = %d, want 2", len(unresolved))
	}

	// First: resolved to 101, pinned to v8, relationship + note carried through.
	if params[0].FromControlID != 101 {
		t.Errorf("params[0] from = %d, want 101", params[0].FromControlID)
	}
	if params[0].ToVersionLabel == nil || *params[0].ToVersionLabel != "v8" {
		t.Errorf("params[0] to_version = %v, want v8", params[0].ToVersionLabel)
	}
	if params[0].Relationship != "related" || params[0].ProvenanceDetail != "n1" {
		t.Errorf("params[0] rel/note = %q/%q", params[0].Relationship, params[0].ProvenanceDetail)
	}

	// Second: case-insensitive from match to 102.
	if params[1].FromControlID != 102 {
		t.Errorf("params[1] from = %d, want 102 (case-insensitive match)", params[1].FromControlID)
	}

	// Third: unpinned target → nil ToVersionLabel.
	if params[2].ToVersionLabel != nil {
		t.Errorf("params[2] to_version = %v, want nil (unpinned)", *params[2].ToVersionLabel)
	}

	// Unresolved preserves the offending rows in order.
	if unresolved[0].FromCitation != "9.9" || unresolved[1].FromCitation != "5.2" {
		t.Errorf("unresolved citations = %q,%q; want 9.9,5.2", unresolved[0].FromCitation, unresolved[1].FromCitation)
	}
}

// recordingQuerier is a fake MapEdgesQuerier that records upserts without a DB.
type recordingQuerier struct {
	upserts  []dbsilver.UpsertControlMappingParams
	resolved int64
}

func (r *recordingQuerier) UpsertControlMapping(_ context.Context, arg dbsilver.UpsertControlMappingParams) error {
	r.upserts = append(r.upserts, arg)
	return nil
}

func (r *recordingQuerier) ResolveControlMappings(_ context.Context) (int64, error) {
	return r.resolved, nil
}

// TestCuratedUpsertWritePath confirms the built params flow to the querier's
// write path exactly once each, with target framework/citation intact.
func TestCuratedUpsertWritePath(t *testing.T) {
	params, _ := buildCuratedUpserts(inventedCuratedRows, inventedFromIndex)
	q := &recordingQuerier{}
	for _, p := range params {
		if err := q.UpsertControlMapping(context.Background(), p); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	if len(q.upserts) != 3 {
		t.Fatalf("recorded upserts = %d, want 3", len(q.upserts))
	}
	if q.upserts[1].ToFrameworkCode != "fwb" || q.upserts[1].ToCitationNorm != "2" {
		t.Errorf("upsert[1] target = %s/%s, want fwb/2", q.upserts[1].ToFrameworkCode, q.upserts[1].ToCitationNorm)
	}
}
