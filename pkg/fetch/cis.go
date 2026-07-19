package fetch

import (
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dannyota/compliary/pkg/operator"
)

// CIS gates the Controls download behind a Pardot registration form (no
// sign-in). The form is parsed live and matched by visible label, not field
// ID, so Pardot ID churn doesn't break the flow. The required consent
// checkbox is the CIS "Terms of Use for Non-Members" agreement — ticking it
// is the operator accepting those terms. The download link itself is
// delivered to the operator's email; retrieving email is never automated.
const cisFormURL = "https://learn.cisecurity.org/cis-controls-download"

// cisConstAnswers are neutral fixed answers for lead-qualification fields.
var cisConstAnswers = map[string]string{
	"How Did You Hear About Us?":                                    "Internet search",
	"Is your organization currently implementing the CIS Controls?": "Unknown",
}

var (
	labelRe    = regexp.MustCompile(`(?s)<label[^>]*for="([^"]+)"[^>]*>(.*?)</label>`)
	tagStripRe = regexp.MustCompile(`<[^>]+>|\s+`)
	fieldRe    = regexp.MustCompile(`<(input|select)[^>]*\bid="%s"[^>]*>`)
	optionRe   = regexp.MustCompile(`<option[^>]*value="([^"]*)"[^>]*>([^<]*)</option>`)
	fieldDivRe = regexp.MustCompile(`<div class="(form-field[^"]*)"[^>]*>`)
	checkboxRe = regexp.MustCompile(`<input type="checkbox"[^>]*name="([^"]+)"[^>]*value="([^"]+)"`)
	errorRe    = regexp.MustCompile(`This field is required`)
	redirectRe = regexp.MustCompile(`top\.location = "([^"]+)"`)
)

// requiredCheckboxes returns name=value for every checkbox whose own
// form-field wrapper is marked required (e.g. the CIS Terms of Use consent).
// Optional checkboxes — marketing opt-ins — are not returned.
func requiredCheckboxes(raw string) map[string]string {
	out := map[string]string{}
	divs := fieldDivRe.FindAllStringSubmatchIndex(raw, -1)
	for i, loc := range divs {
		end := len(raw)
		if i+1 < len(divs) {
			end = divs[i+1][0]
		}
		class := raw[loc[2]:loc[3]]
		if !strings.Contains(class, "required") {
			continue
		}
		if m := checkboxRe.FindStringSubmatch(raw[loc[0]:end]); m != nil {
			out[m[1]] = m[2]
		}
	}
	return out
}

// cisMarker records that the form was already submitted and the emailed
// link is still pending, so re-runs don't re-submit.
const cisMarker = "AWAITING-EMAIL.txt"

// CIS submits the registration form as the operator. The download link
// arrives at the operator's email address; instructions are reported.
func CIS(c *http.Client, dataDir string, id *operator.Identity, report func(string)) error {
	destDir := filepath.Join(dataDir, "cis")
	entries, _ := os.ReadDir(destDir)
	awaiting := false
	for _, e := range entries {
		if e.Name() == cisMarker {
			awaiting = true
			continue
		}
		report("skip (exists): data/cis already has files")
		return nil
	}
	if awaiting {
		report("form already submitted — check the operator inbox and save the files into " + destDir + "/")
		return nil
	}

	page, err := getBody(c, cisFormURL)
	if err != nil {
		return fmt.Errorf("cis form page: %w", err)
	}
	raw := string(page)

	form := url.Values{"_utf8": {"☃"}, "hiddenDependentFields": {""}, "pi_extra_field": {""}}

	// Text fields by label.
	for label, value := range map[string]string{
		"First Name":   id.FirstName,
		"Last Name":    id.LastName,
		"Organization": id.Company,
		"Email":        id.Email,
	} {
		name, _, err := fieldByLabel(raw, label)
		if err != nil {
			return fmt.Errorf("cis form: %w", err)
		}
		form.Set(name, value)
	}

	// Selects by label, matched against visible option text.
	selects := map[string]string{
		"Sector":                    sectorChoice(id.Industry),
		"Country":                   id.Country,
		"Number of Employees Range": id.Employees,
	}
	maps.Copy(selects, cisConstAnswers)
	for label, want := range selects {
		name, tag, err := fieldByLabel(raw, label)
		if err != nil {
			return fmt.Errorf("cis form: %w", err)
		}
		val, err := optionValue(tag, want)
		if err != nil {
			return fmt.Errorf("cis form %q: %w", label, err)
		}
		form.Set(name, val)
	}

	// Required consent checkboxes (Terms of Use). Optional marketing
	// checkboxes stay unchecked.
	consents := requiredCheckboxes(raw)
	if len(consents) == 0 {
		return fmt.Errorf("cis form: required consent checkbox not found")
	}
	for name, value := range consents {
		form.Set(name, value)
	}

	req, err := http.NewRequest(http.MethodPost, cisFormURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("cis build submit: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", cisFormURL)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("cis submit: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cis submit response: %w", err)
	}
	if errorRe.Match(body) {
		return fmt.Errorf("cis submit rejected: form reports required fields missing")
	}
	if !redirectRe.Match(body) {
		return fmt.Errorf("cis submit: no thank-you redirect in response")
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", destDir, err)
	}
	instructions := "CIS emails the download link:\n" +
		"  1. check the operator inbox (may take ~10 min, check spam)\n" +
		"  2. click the download link in the email\n" +
		"  3. save the files into " + destDir + "/ (then delete this file)\n"
	if err := os.WriteFile(filepath.Join(destDir, cisMarker), []byte(instructions), 0o644); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	report("form submitted — " + instructions)
	return nil
}

// fieldByLabel resolves a visible label to its form field name and full tag.
func fieldByLabel(raw, label string) (name, tag string, err error) {
	for _, m := range labelRe.FindAllStringSubmatch(raw, -1) {
		text := strings.TrimSpace(tagStripRe.ReplaceAllString(html.UnescapeString(m[2]), " "))
		if !strings.EqualFold(strings.TrimSpace(text), label) {
			continue
		}
		re := regexp.MustCompile(strings.Replace(fieldRe.String(), "%s", regexp.QuoteMeta(m[1]), 1))
		fm := re.FindString(raw)
		if fm == "" {
			continue
		}
		nm := regexp.MustCompile(`name="([^"]+)"`).FindStringSubmatch(fm)
		if nm == nil {
			continue
		}
		if strings.HasPrefix(fm, "<select") {
			// capture the whole select block for option parsing
			block := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(fm) + `.*?</select>`).FindString(raw)
			return nm[1], block, nil
		}
		return nm[1], fm, nil
	}
	return "", "", fmt.Errorf("field labeled %q not found", label)
}

// optionValue picks the option whose visible text contains want
// (case-insensitive), preferring an exact match.
func optionValue(selectBlock, want string) (string, error) {
	want = strings.ToLower(strings.TrimSpace(want))
	contains := ""
	for _, m := range optionRe.FindAllStringSubmatch(selectBlock, -1) {
		text := strings.ToLower(strings.TrimSpace(html.UnescapeString(m[2])))
		if text == "" || m[1] == "" {
			continue
		}
		if text == want {
			return m[1], nil
		}
		if contains == "" && strings.Contains(text, want) {
			contains = m[1]
		}
	}
	if contains != "" {
		return contains, nil
	}
	// last resort: an "Other" option
	for _, m := range optionRe.FindAllStringSubmatch(selectBlock, -1) {
		if strings.EqualFold(strings.TrimSpace(m[2]), "other") {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("no option matching %q", want)
}

// sectorChoice maps the operator's industry preference to CIS sector text.
func sectorChoice(industry string) string {
	switch strings.ToLower(strings.TrimSpace(industry)) {
	case "financial":
		return "Financial Services"
	case "technology":
		return "Technology"
	default:
		return "Other"
	}
}
