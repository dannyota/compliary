package fetch

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// CIS publishes the Critical Security Controls on a public download page
// (the destination of their registration email — no form, no sign-in).
// Use is governed by the CIS non-member Terms of Use (CC BY-NC-ND 4.0 for
// the Controls): attribution, non-commercial, unmodified.
const cisPageURL = "https://learn.cisecurity.org/control-download-v8-1"

// cisLegacyMarker is left over from the retired form-and-email flow;
// remove it when found so the directory reflects real content only.
const cisLegacyMarker = "AWAITING-EMAIL.txt"

var (
	anchorRe     = regexp.MustCompile(`(?s)<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	embedRe      = regexp.MustCompile(`(?:src|href)='([^']+\.pdf)'`)
	tagOrSpaceRe = regexp.MustCompile(`<[^>]+>|\s+`)
	separatorRe  = regexp.MustCompile(`[_\s]+`)
	dashRunRe    = regexp.MustCompile(`-{2,}`)
)

// kebabName normalizes a publisher filename to the data/ naming convention:
// lowercase kebab-case, no underscores or spaces.
func kebabName(name string) string {
	name = separatorRe.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(dashRunRe.ReplaceAllString(name, "-"), "-")
}

// anchorText flattens anchor inner HTML to comparable plain text.
func anchorText(inner string) string {
	return strings.TrimSpace(tagOrSpaceRe.ReplaceAllString(html.UnescapeString(inner), " "))
}

// CIS downloads the Controls guide PDF and the Excel workbooks (controls +
// change log) from the public download page.
func CIS(c *http.Client, dataDir string, report func(string)) error {
	destDir := filepath.Join(dataDir, "cis")
	_ = os.Remove(filepath.Join(destDir, cisLegacyMarker))
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
		report("skip (exists): data/cis already has files")
		return nil
	}

	page, err := getBody(c, cisPageURL)
	if err != nil {
		return fmt.Errorf("cis page: %w", err)
	}

	var pdfPages, excels []string
	for _, m := range anchorRe.FindAllStringSubmatch(string(page), -1) {
		text := anchorText(m[2])
		switch {
		case strings.HasPrefix(text, "Download PDF"):
			pdfPages = append(pdfPages, m[1])
		case strings.HasPrefix(text, "Download Excel"):
			excels = append(excels, m[1])
		}
	}
	if len(pdfPages) == 0 || len(excels) == 0 {
		return fmt.Errorf("cis page: download links not found (pdf %d, excel %d)", len(pdfPages), len(excels))
	}

	// The PDF anchor may return a small HTML viewer embedding the real file
	// (browser UAs) or the PDF directly (non-browser UAs, observed 2026-07-21).
	// Peek at the first bytes to distinguish the two cases.
	pdfResp, err := get(c, pdfPages[0])
	if err != nil {
		return fmt.Errorf("cis pdf: %w", err)
	}
	head := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(pdfResp.Body, head); err != nil {
		pdfResp.Body.Close()
		return fmt.Errorf("cis pdf peek: %w", err)
	}
	// Reconstruct the full body with the peeked bytes prepended.
	pdfResp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), pdfResp.Body))
	if bytes.Equal(head, pdfMagic) {
		// Direct PDF response.
		defer pdfResp.Body.Close()
		name := resolvedName(pdfResp, "cis-controls.pdf")
		dest := filepath.Join(destDir, name)
		if exists(dest) {
			report("skip (exists): cis/" + name)
		} else {
			if err := saveResponse(pdfResp, dest, pdfMagic); err != nil {
				return fmt.Errorf("cis pdf: %w", err)
			}
			report("downloaded: cis/" + name)
		}
	} else {
		// HTML viewer page — extract the embedded PDF URL.
		viewer, err := io.ReadAll(pdfResp.Body)
		pdfResp.Body.Close()
		if err != nil {
			return fmt.Errorf("cis pdf viewer read: %w", err)
		}
		m := embedRe.FindSubmatch(viewer)
		if m == nil {
			return fmt.Errorf("cis pdf viewer: embedded pdf url not found")
		}
		if err := saveByFinalName(c, string(m[1]), destDir, pdfMagic, report); err != nil {
			return fmt.Errorf("cis pdf: %w", err)
		}
	}
	for _, u := range excels {
		if err := saveByFinalName(c, u, destDir, []byte("PK"), report); err != nil {
			return fmt.Errorf("cis excel: %w", err)
		}
	}
	return nil
}

// cisMappingFiles are the CIS-published mapping workbooks (same CC BY-NC-ND
// terms as the Controls). The learn.cisecurity.org URLs are direct XLSX
// downloads; destinations are pinned so file_rule patterns stay stable.
var cisMappingFiles = []struct {
	url  string
	dest string
}{
	{"https://learn.cisecurity.org/controls-v8.1-mapping-iso-iec-27001-2022", "cis/cis-controls-v8.1-mapping-to-iso-iec-27001-2022.xlsx"},
	{"https://learn.cisecurity.org/cis-controls-v8.1-mapping-nist-csf-v2", "cis/cis-controls-v8.1-mapping-to-nist-csf-2.0.xlsx"},
	{"https://learn.cisecurity.org/controls-v8.1-mapping-nist-sp-800-53-rev5", "cis/cis-controls-v8.1-mapping-to-nist-sp-800-53-r5.xlsx"},
}

// CISMappings downloads the CIS mapping workbooks. Runs after CIS() and uses
// per-file existence checks (CIS() skips when data/cis is non-empty, which
// would strand these files on corpora fetched before mappings were added).
func CISMappings(c *http.Client, dataDir string, report func(string)) error {
	for _, f := range cisMappingFiles {
		dest := filepath.Join(dataDir, f.dest)
		if exists(dest) {
			report("skip (exists): " + f.dest)
			continue
		}
		if err := downloadFile(c, f.url, dest, []byte("PK")); err != nil {
			return fmt.Errorf("cis mappings: %w", err)
		}
		report("downloaded: " + f.dest)
	}
	return nil
}

// resolvedName returns a kebab-case filename from the response's final
// (redirect-resolved) URL, falling back to fallback if the URL has no
// usable basename.
func resolvedName(resp *http.Response, fallback string) string {
	final := resp.Request.URL
	name, err := url.PathUnescape(path.Base(final.Path))
	if err != nil || name == "" || name == "/" || name == "." {
		return fallback
	}
	return kebabName(name)
}

// saveByFinalName downloads u and names the file after the redirect-resolved
// URL's basename (the CIS links resolve to versioned storage filenames),
// normalized to the data/ kebab-case convention. The response body is
// consumed directly — no second request is issued.
func saveByFinalName(c *http.Client, u, destDir string, magic []byte, report func(string)) error {
	resp, err := get(c, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	final := resp.Request.URL
	name, err := url.PathUnescape(path.Base(final.Path))
	if err != nil || name == "" || name == "/" || name == "." {
		return fmt.Errorf("get %s: no usable filename in %s", u, final)
	}
	name = kebabName(name)
	dest := filepath.Join(destDir, name)
	if exists(dest) {
		report("skip (exists): cis/" + name)
		return nil
	}
	if err := saveResponse(resp, dest, magic); err != nil {
		return err
	}
	report("downloaded: cis/" + name)
	return nil
}
