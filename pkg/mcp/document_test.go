package mcp

import "testing"

func TestNormalizeCitationInput(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AC-2 (3)", "AC-2(3)"},   // space before parens
		{"ac-2 ( 3 )", "ac-2(3)"}, // scattered spaces
		{"AC-2.3", "AC-2(3)"},     // dot-separated enhancement
		{"PM-5", "PM-5"},          // untouched
		{"8.3.6", "8.3.6"},        // PCI dotted decimal untouched
		{"7.5.1", "7.5.1"},        // ISO clause untouched
		{"A.5.1", "A.5.1"},        // annex untouched
		{"CC6.1", "CC6.1"},        // TSC untouched
		{"EDM01.01", "EDM01.01"},  // COBIT practice untouched (letter prefix but no dash)
	}
	for _, c := range cases {
		if got := normalizeCitationInput(c.in); got != c.want {
			t.Errorf("normalizeCitationInput(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
