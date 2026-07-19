package operator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const envComplete = `# comment
COMPLIARY_FIRST_NAME=Jane
COMPLIARY_LAST_NAME=Doe
COMPLIARY_TITLE=Security Engineer
COMPLIARY_COMPANY=Example Corp
COMPLIARY_COUNTRY=Vietnam
COMPLIARY_EMAIL=jane@example.com
COMPLIARY_EMPLOYEES=50 to 99
COMPLIARY_INDUSTRY=financial
`

func TestLoadComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(envComplete), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := Load(path, strings.NewReader(""), &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if id.FullName() != "Jane Doe" {
		t.Errorf("FullName = %q", id.FullName())
	}
	if id.Country != "Vietnam" || id.Industry != "financial" || id.Employees != "50 to 99" {
		t.Errorf("fields: %+v", id)
	}
}

func TestLoadPromptsAndSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	partial := strings.Replace(envComplete, "COMPLIARY_EMAIL=jane@example.com\n", "", 1)
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	id, err := Load(path, strings.NewReader("jane@example.com\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "jane@example.com" {
		t.Errorf("Email = %q", id.Email)
	}
	if !strings.Contains(out.String(), "Email") {
		t.Errorf("prompt output: %q", out.String())
	}

	// The answer is cached: a second load with empty stdin succeeds.
	id2, err := Load(path, strings.NewReader(""), &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	if id2.Email != "jane@example.com" {
		t.Errorf("cached Email = %q", id2.Email)
	}
}

func TestLoadEmptyAnswerFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if _, err := Load(path, strings.NewReader("\n"), &strings.Builder{}); err == nil {
		t.Error("expected error for empty required answer")
	}
}
