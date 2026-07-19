package fetch

import (
	"encoding/json"
	"testing"
)

// Shapes mirror doc_library.json: single-object and array forms both occur.
const libSnippet = `[
  {
    "reference": "pcidss",
    "document": {
      "name": "PCI DSS",
      "agreement": "pcidss",
      "protected": "yes",
      "last_updated": "2024-06-11T07:00:00+00:00",
      "versions": {
        "version": [
          {"title": "v4.0.1", "files": {"file": {"path": "/PCI%20DSS/Standard/PCI-DSS-v4_0_1.pdf"}}},
          {"title": "v4.0", "files": {"file": [{"path": "/PCI%20DSS/Standard/PCI-DSS-v4_0.pdf"}]}}
        ]
      }
    }
  }
]`

func TestAgreementKey(t *testing.T) {
	var lib pciLibrary
	if err := json.Unmarshal([]byte(libSnippet), &lib); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	key, err := agreementKey(lib, "/PCI%20DSS/Standard/PCI-DSS-v4_0_1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	want := "ip:pcidss:v4.0.1:2024-06-11T07:00:00+00:00"
	if key != want {
		t.Errorf("got %q, want %q", key, want)
	}

	// An older version still keys on the latest version's title.
	key, err = agreementKey(lib, "/PCI%20DSS/Standard/PCI-DSS-v4_0.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if key != want {
		t.Errorf("old version: got %q, want %q", key, want)
	}

	if _, err := agreementKey(lib, "/nope.pdf"); err == nil {
		t.Error("expected error for unknown path")
	}
}

func TestOneOrMany(t *testing.T) {
	var single oneOrMany[pciFile]
	if err := json.Unmarshal([]byte(`{"path":"/a"}`), &single); err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || single[0].Path != "/a" {
		t.Errorf("single: %v", single)
	}

	var many oneOrMany[pciFile]
	if err := json.Unmarshal([]byte(`[{"path":"/a"},{"path":"/b"}]`), &many); err != nil {
		t.Fatal(err)
	}
	if len(many) != 2 || many[1].Path != "/b" {
		t.Errorf("many: %v", many)
	}
}
