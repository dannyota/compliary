package fetch

import (
	"fmt"
	"html"
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
)

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

	// The PDF anchor points at a small viewer page embedding the real file.
	viewer, err := getBody(c, pdfPages[0])
	if err != nil {
		return fmt.Errorf("cis pdf viewer: %w", err)
	}
	m := embedRe.FindSubmatch(viewer)
	if m == nil {
		return fmt.Errorf("cis pdf viewer: embedded pdf url not found")
	}
	if err := saveByFinalName(c, string(m[1]), destDir, pdfMagic, report); err != nil {
		return fmt.Errorf("cis pdf: %w", err)
	}
	for _, u := range excels {
		if err := saveByFinalName(c, u, destDir, []byte("PK"), report); err != nil {
			return fmt.Errorf("cis excel: %w", err)
		}
	}
	return nil
}

// saveByFinalName downloads u and names the file after the redirect-resolved
// URL's basename (the CIS links resolve to versioned storage filenames).
func saveByFinalName(c *http.Client, u, destDir string, magic []byte, report func(string)) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", u, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", u, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: status %s", u, resp.Status)
	}
	final := resp.Request.URL
	name, err := url.PathUnescape(path.Base(final.Path))
	if err != nil || name == "" || name == "/" || name == "." {
		return fmt.Errorf("get %s: no usable filename in %s", u, final)
	}
	dest := filepath.Join(destDir, name)
	if exists(dest) {
		report("skip (exists): cis/" + name)
		return nil
	}
	if err := downloadFile(c, final.String(), dest, magic); err != nil {
		return err
	}
	report("downloaded: cis/" + name)
	return nil
}
