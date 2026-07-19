package fetch

import (
	"strings"
	"testing"
)

const cisPageSnippet = `
<p>Version 8.1</p>
<a href="https://learn.example/cis-controls-v8_1_guide_pdf"><strong>Download PDF</strong> &rarr;</a>
<a href="https://learn.example/l/1/a">Download Excel &rarr;</a>
<p>Change Log:</p>
<a href="https://learn.example/l/1/b">Download Excel &rarr;</a>
<p>Translations:</p>
<a href="https://learn.example/l/1/fr">Download French &rarr;</a>
<a href="https://learn.example/l/1/icon.png"><img/></a>`

func TestCISLinkExtraction(t *testing.T) {
	var pdfPages, excels []string
	for _, m := range anchorRe.FindAllStringSubmatch(cisPageSnippet, -1) {
		text := anchorText(m[2])
		switch {
		case strings.HasPrefix(text, "Download PDF"):
			pdfPages = append(pdfPages, m[1])
		case strings.HasPrefix(text, "Download Excel"):
			excels = append(excels, m[1])
		}
	}
	if len(pdfPages) != 1 || pdfPages[0] != "https://learn.example/cis-controls-v8_1_guide_pdf" {
		t.Errorf("pdf pages: %v", pdfPages)
	}
	if len(excels) != 2 || excels[0] != "https://learn.example/l/1/a" || excels[1] != "https://learn.example/l/1/b" {
		t.Errorf("excel links (translations must be excluded): %v", excels)
	}
}

func TestKebabName(t *testing.T) {
	cases := map[string]string{
		"CIS_Controls_Guide_v8.1.2_0325_v2.pdf":                   "cis-controls-guide-v8.1.2-0325-v2.pdf",
		"CIS_Controls_Version_8.1.2___March_2025.xlsx":            "cis-controls-version-8.1.2-march-2025.xlsx",
		"CIS_Controls_Version_8.1.2_Change_Log___March_2025.xlsx": "cis-controls-version-8.1.2-change-log-march-2025.xlsx",
		"plain-name.pdf": "plain-name.pdf",
	}
	for in, want := range cases {
		if got := kebabName(in); got != want {
			t.Errorf("kebabName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmbedRe(t *testing.T) {
	viewer := `<embed type='application/pdf' src='https://storage.example/1/CIS_Controls_Guide_v8.1.2.pdf' width='100%'/>`
	m := embedRe.FindStringSubmatch(viewer)
	if m == nil || m[1] != "https://storage.example/1/CIS_Controls_Guide_v8.1.2.pdf" {
		t.Errorf("embed match: %v", m)
	}
}
