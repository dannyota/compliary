package fetch

import "testing"

// A trimmed Pardot form: labeled text input, labeled select, an optional
// marketing checkbox, and a required consent checkbox.
const cisFormSnippet = `
<div class="form-field FirstName required">
<label class="field-label" for="f_1">First Name</label>
<input type="text" name="n_first" id="f_1" value=""/>
</div>
<div class="form-field Sector pd-select required">
<label class="field-label" for="f_2">Sector</label>
<select name="n_sector" id="f_2" class="select"><option value="" selected></option><option value="10">Financial Services</option><option value="11">Technology</option><option value="12">Other</option></select>
</div>
<div class="form-field  Opt_into_Marketing_Email pd-checkbox   no-label  ">
<span class="value"><span><input type="checkbox" name="n_marketing" id="f_3" value="31"><label class="inline" for="f_3"><span>Text</span></label></span></span>
</div>
<div class="form-field  I_Have_Read_These pd-checkbox required  no-label  ">
<span class="value"><span><input type="checkbox" name="n_consent" id="f_4" value="32"><label class="inline" for="f_4"><span>Text</span></label></span></span>
</div>`

func TestFieldByLabel(t *testing.T) {
	tests := []struct {
		label    string
		wantName string
	}{
		{"First Name", "n_first"},
		{"Sector", "n_sector"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			name, _, err := fieldByLabel(cisFormSnippet, tt.label)
			if err != nil {
				t.Fatalf("fieldByLabel(%q): %v", tt.label, err)
			}
			if name != tt.wantName {
				t.Errorf("got %q, want %q", name, tt.wantName)
			}
		})
	}
	if _, _, err := fieldByLabel(cisFormSnippet, "Nonexistent"); err == nil {
		t.Error("expected error for missing label")
	}
}

func TestOptionValue(t *testing.T) {
	_, sel, err := fieldByLabel(cisFormSnippet, "Sector")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		want  string
		value string
	}{
		{"Financial Services", "10"},
		{"financial", "10"},
		{"Technology", "11"},
		{"no such sector", "12"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := optionValue(sel, tt.want)
			if err != nil {
				t.Fatalf("optionValue(%q): %v", tt.want, err)
			}
			if got != tt.value {
				t.Errorf("got %q, want %q", got, tt.value)
			}
		})
	}
}

func TestRequiredCheckboxes(t *testing.T) {
	got := requiredCheckboxes(cisFormSnippet)
	if len(got) != 1 {
		t.Fatalf("got %d checkboxes, want 1 (consent only): %v", len(got), got)
	}
	if got["n_consent"] != "32" {
		t.Errorf("consent checkbox: got %v", got)
	}
	if _, ok := got["n_marketing"]; ok {
		t.Error("marketing opt-in must not be checked")
	}
}

func TestSectorChoice(t *testing.T) {
	tests := []struct{ in, want string }{
		{"financial", "Financial Services"},
		{"technology", "Technology"},
		{"", "Other"},
		{"something else", "Other"},
	}
	for _, tt := range tests {
		if got := sectorChoice(tt.in); got != tt.want {
			t.Errorf("sectorChoice(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
