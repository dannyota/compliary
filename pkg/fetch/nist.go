package fetch

import (
	"fmt"
	"net/http"
	"path/filepath"
)

var pdfMagic = []byte("%PDF-")

// nistFiles are public-domain (17 U.S.C. §105) direct downloads.
var nistFiles = []struct {
	url   string
	dest  string
	magic []byte
}{
	{"https://nvlpubs.nist.gov/nistpubs/CSWP/NIST.CSWP.29.pdf", "nist/NIST.CSWP.29-csf-2.0.pdf", pdfMagic},
	{"https://csrc.nist.gov/extensions/nudp/services/json/csf/download?olirids=all", "nist/csf-2.0.xlsx", []byte("PK")},
	{"https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-53r5.pdf", "nist/NIST.SP.800-53r5.pdf", pdfMagic},
	{"https://raw.githubusercontent.com/usnistgov/oscal-content/main/nist.gov/SP800-53/rev5/json/NIST_SP-800-53_rev5_catalog.json", "nist/NIST_SP-800-53_rev5_catalog.json", []byte(`{`)},
}

// NIST downloads the NIST CSF 2.0 and SP 800-53 r5 documents into dataDir.
func NIST(c *http.Client, dataDir string, report func(string)) error {
	for _, f := range nistFiles {
		dest := filepath.Join(dataDir, f.dest)
		if exists(dest) {
			report("skip (exists): " + f.dest)
			continue
		}
		if err := downloadFile(c, f.url, dest, f.magic); err != nil {
			return fmt.Errorf("nist: %w", err)
		}
		report("downloaded: " + f.dest)
	}
	return nil
}
