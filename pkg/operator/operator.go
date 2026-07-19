// Package operator loads the operator identity used to fill publisher
// download forms. Values live in a gitignored env file and must never be
// written to logs, commits, or committed files.
package operator

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Identity is the operator's real contact information. Treat every field as
// a secret: submit to official publisher forms only, never log values.
type Identity struct {
	FirstName string
	LastName  string
	Title     string
	Company   string
	Country   string
	Email     string

	Phone     string
	Website   string
	Address1  string
	Address2  string
	City      string
	State     string
	Zip       string
	Industry  string // financial | technology | other
	Employees string // e.g. "250 to 499"
}

// FullName is the "Contact Name" form of the identity.
func (id *Identity) FullName() string {
	return strings.TrimSpace(id.FirstName + " " + id.LastName)
}

var envKeys = map[string]func(*Identity) *string{
	"COMPLIARY_FIRST_NAME": func(id *Identity) *string { return &id.FirstName },
	"COMPLIARY_LAST_NAME":  func(id *Identity) *string { return &id.LastName },
	"COMPLIARY_TITLE":      func(id *Identity) *string { return &id.Title },
	"COMPLIARY_COMPANY":    func(id *Identity) *string { return &id.Company },
	"COMPLIARY_COUNTRY":    func(id *Identity) *string { return &id.Country },
	"COMPLIARY_EMAIL":      func(id *Identity) *string { return &id.Email },
	"COMPLIARY_PHONE":      func(id *Identity) *string { return &id.Phone },
	"COMPLIARY_WEBSITE":    func(id *Identity) *string { return &id.Website },
	"COMPLIARY_ADDRESS1":   func(id *Identity) *string { return &id.Address1 },
	"COMPLIARY_ADDRESS2":   func(id *Identity) *string { return &id.Address2 },
	"COMPLIARY_CITY":       func(id *Identity) *string { return &id.City },
	"COMPLIARY_STATE":      func(id *Identity) *string { return &id.State },
	"COMPLIARY_ZIP":        func(id *Identity) *string { return &id.Zip },
	"COMPLIARY_INDUSTRY":   func(id *Identity) *string { return &id.Industry },
	"COMPLIARY_EMPLOYEES":  func(id *Identity) *string { return &id.Employees },
}

// required lists the keys prompted for when missing, with prompt hints.
var required = []struct{ key, hint string }{
	{"COMPLIARY_FIRST_NAME", "First name"},
	{"COMPLIARY_LAST_NAME", "Last name"},
	{"COMPLIARY_TITLE", "Job title"},
	{"COMPLIARY_COMPANY", "Company name"},
	{"COMPLIARY_COUNTRY", "Country (e.g. Vietnam)"},
	{"COMPLIARY_EMAIL", "Email"},
	{"COMPLIARY_EMPLOYEES", `Company size range (e.g. "50 to 99", "250 to 499")`},
}

// Load reads the env file, prompts on in/out for any missing required keys,
// and appends the answers back to the file so later runs skip the prompts.
func Load(path string, in io.Reader, out io.Writer) (*Identity, error) {
	values := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	var missing []string
	reader := bufio.NewReader(in)
	for _, req := range required {
		if values[req.key] != "" {
			continue
		}
		fmt.Fprintf(out, "%s: ", req.hint)
		answer, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", req.key, err)
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return nil, fmt.Errorf("%s is required", req.key)
		}
		values[req.key] = answer
		missing = append(missing, req.key+"="+answer)
	}
	if len(missing) > 0 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("append env file: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString("\n" + strings.Join(missing, "\n") + "\n"); err != nil {
			return nil, fmt.Errorf("write env file: %w", err)
		}
	}

	id := &Identity{}
	for key, field := range envKeys {
		*field(id) = values[key]
	}
	return id, nil
}
