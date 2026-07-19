package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// tscCapture mirrors extract.PDFCapture for parsing pdf-pages-json.
type tscCapture struct {
	Pages []tscPage `json:"pages"`
}

type tscPage struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// reTSCCriterion matches TSC criterion IDs at line start.
// Series: CC (common criteria), A (availability), C (confidentiality),
// PI (processing integrity), P (privacy).
// Format: CC1.1, A1.2, C1.1, PI1.4, P1.1, P6.7, etc.
var reTSCCriterion = regexp.MustCompile(`^(CC\d+\.\d+|A\d+\.\d+|C\d+\.\d+|PI\d+\.\d+|P\d+\.\d+)\s`)

// reTSCCriterionMidLine matches a criterion ID appearing mid-line.
// PI1.1 appears after the "ADDITIONAL CRITERIA FOR PROCESSING INTEGRITY..."
// section header on the same line in the go-fitz capture.
var reTSCCriterionMidLine = regexp.MustCompile(`\s(CC\d+\.\d+|A\d+\.\d+|C\d+\.\d+|PI\d+\.\d+|P\d+\.\d+)\s`)

// reTSCPrivacyCategory matches Px.0 privacy category headers (P1.0, P2.0, ...).
// These are NOT criteria — they are section groupings. The official 2017 TSC has
// 18 P-series criteria (P1.1, P2.1, ..., P8.1); the Px.0 rows are headers only.
var reTSCPrivacyCategory = regexp.MustCompile(`^P\d+\.0\s`)

// reTSCPoFBullet matches a point-of-focus bullet line starting with "•".
var reTSCPoFBullet = regexp.MustCompile(`^•\s+`)

// reTSCPoFLeadIn extracts the bold lead-in phrase from a PoF bullet.
// Format: "• Lead-In Phrase — description text" or "• Lead-In Phrase [C] — ..."
var reTSCPoFLeadIn = regexp.MustCompile(`^•\s+(.+?)\s*(?:\[[CP](?:,\s*[CP])?\])?\s*[—–-]\s+`)

// tscParsedCriterion is an intermediate result from parsing the capture.
type tscParsedCriterion struct {
	Citation string
	Page     int
	Body     string // criterion normative text
	PoFs     []tscParsedPoF
}

// tscParsedPoF is an intermediate PoF parsed from bullets under a criterion.
type tscParsedPoF struct {
	LeadIn string // bold lead-in phrase (for title_original, auth-gated)
	Body   string // full PoF text including lead-in
}

// BuildTSCTree parses a pdf-pages-json capture for AICPA TSC 2017 (with 2022
// revised points of focus) and returns the normalized control tree. Pure function.
//
// Tree shape per design decision 2: criteria = kind 'criterion', flat roots
// (no series grouping). Points of focus = kind 'point-of-focus' children,
// citation <criterion>-pof-NN (zero-padded ordinal, document order).
// Title = neutral "Criterion <citation>" / "<citation> point of focus N".
// title_original = nil for criteria; PoF bold lead-in phrase (auth-gated) for PoFs.
// Body = criterion normative text (auth-gated) / PoF text (auth-gated).
func BuildTSCTree(raw json.RawMessage, _, versionLabel string) (*TreeResult, error) {
	var cap tscCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	criteria, err := parseTSCPages(cap.Pages)
	if err != nil {
		return nil, err
	}

	if len(criteria) == 0 {
		return nil, fmt.Errorf("no criteria found in capture")
	}

	return buildTSCControlTree(criteria, versionLabel)
}

// parseTSCPages scans all pages for criterion IDs and PoF bullets, recovering
// mid-line criterion IDs (PI1.1) and filtering out Px.0 privacy category headers.
func parseTSCPages(pages []tscPage) ([]tscParsedCriterion, error) {
	// Collect all lines with page numbers.
	type lineInfo struct {
		page int
		text string
	}
	var allLines []lineInfo

	// Build set of line-start criterion IDs for mid-line recovery.
	lineStartIDs := make(map[string]bool)
	for _, page := range pages {
		for _, line := range strings.Split(page.Text, "\n") {
			stripped := strings.TrimSpace(line)
			if stripped == "" {
				continue
			}
			if reTSCPrivacyCategory.MatchString(stripped) {
				continue // Px.0 headers are not criteria
			}
			if m := reTSCCriterion.FindStringSubmatch(stripped); m != nil {
				lineStartIDs[m[1]] = true
			}
		}
	}

	// Process all lines, splitting mid-line criterion IDs.
	for _, page := range pages {
		for _, line := range strings.Split(page.Text, "\n") {
			stripped := strings.TrimSpace(line)
			if stripped == "" {
				allLines = append(allLines, lineInfo{page: page.N, text: ""})
				continue
			}

			// Skip Px.0 privacy category headers.
			if reTSCPrivacyCategory.MatchString(stripped) {
				continue
			}

			// Check for mid-line criterion ID.
			split := false
			if loc := reTSCCriterionMidLine.FindStringIndex(stripped); loc != nil {
				candidate := reTSCCriterionMidLine.FindStringSubmatch(stripped)[1]
				if loc[0] > 0 && !lineStartIDs[candidate] {
					// This ID never appears at line-start — it's a mid-line criterion
					// (e.g., PI1.1 after section header text).
					before := strings.TrimSpace(stripped[:loc[0]])
					after := strings.TrimSpace(stripped[loc[0]+1:]) // +1 to skip space
					if before != "" {
						allLines = append(allLines, lineInfo{page: page.N, text: before})
					}
					allLines = append(allLines, lineInfo{page: page.N, text: after})
					split = true
				}
			}

			if !split {
				allLines = append(allLines, lineInfo{page: page.N, text: stripped})
			}
		}
	}

	// Parse criteria and their PoFs.
	var criteria []tscParsedCriterion
	var current *tscParsedCriterion
	var bodyParts []string
	var currentPoFParts []string
	var currentPoFLeadIn string
	inPoF := false

	flushPoF := func() {
		if current != nil && inPoF && len(currentPoFParts) > 0 {
			current.PoFs = append(current.PoFs, tscParsedPoF{
				LeadIn: currentPoFLeadIn,
				Body:   strings.Join(currentPoFParts, "\n"),
			})
		}
		currentPoFParts = nil
		currentPoFLeadIn = ""
		inPoF = false
	}

	flushCriterion := func() {
		flushPoF()
		if current != nil {
			current.Body = strings.TrimSpace(strings.Join(bodyParts, "\n"))
			criteria = append(criteria, *current)
		}
		bodyParts = nil
		current = nil
	}

	// Lines we skip in the criteria-detection phase.
	isPreambleLine := func(text string) bool {
		// Filter out structural artifacts from the table layout.
		upper := strings.ToUpper(strings.TrimSpace(text))
		return upper == "TSP REF. #" ||
			upper == "TRUST SERVICES CRITERIA AND POINTS OF FOCUS" ||
			strings.HasPrefix(upper, "CONTROL ENVIRONMENT") ||
			strings.HasPrefix(upper, "RISK ASSESSMENT") ||
			strings.HasPrefix(upper, "MONITORING ACTIVITIES") ||
			strings.HasPrefix(upper, "LOGICAL AND PHYSICAL ACCESS CONTROLS") ||
			strings.HasPrefix(upper, "SYSTEM OPERATIONS") ||
			strings.HasPrefix(upper, "CHANGE MANAGEMENT") ||
			strings.HasPrefix(upper, "RISK MITIGATION") ||
			strings.HasPrefix(upper, "ADDITIONAL CRITERIA FOR") ||
			strings.HasPrefix(upper, "COMMON CRITERIA") ||
			strings.HasPrefix(upper, "THE FOLLOWING POINTS OF FOCUS") ||
			strings.HasPrefix(upper, "THE FOLLOWING POINT OF FOCUS") ||
			strings.HasPrefix(upper, "POINTS OF FOCUS SPECIFIED") ||
			strings.HasPrefix(upper, "ADDITIONAL POINT") ||
			strings.HasPrefix(upper, "ADDITIONAL POINTS") ||
			strings.HasPrefix(upper, "PAGE ")
	}

	for _, li := range allLines {
		text := li.text

		// Check for criterion ID at line start.
		if m := reTSCCriterion.FindStringSubmatch(text); m != nil {
			flushCriterion()
			current = &tscParsedCriterion{
				Citation: m[1],
				Page:     li.page,
			}
			// Rest of line after the criterion ID is part of the body.
			rest := strings.TrimSpace(text[len(m[0])-1:]) // -1 to not include trailing space from regex
			rest = strings.TrimSpace(rest)
			if rest != "" {
				bodyParts = append(bodyParts, rest)
			}
			continue
		}

		if current == nil {
			continue // before first criterion
		}

		// Check for PoF bullet.
		if reTSCPoFBullet.MatchString(text) {
			flushPoF()
			inPoF = true

			// Extract lead-in phrase.
			if m := reTSCPoFLeadIn.FindStringSubmatch(text); m != nil {
				currentPoFLeadIn = strings.TrimSpace(m[1])
			}

			// Strip the bullet marker for body text.
			pofText := strings.TrimSpace(strings.TrimPrefix(text, "•"))
			pofText = strings.TrimSpace(pofText)
			currentPoFParts = append(currentPoFParts, pofText)
			continue
		}

		// Continuation of current context.
		if inPoF {
			// PoF continuation line.
			if !isPreambleLine(text) && text != "" {
				currentPoFParts = append(currentPoFParts, text)
			}
		} else {
			// Criterion body continuation.
			if !isPreambleLine(text) && text != "" {
				bodyParts = append(bodyParts, text)
			}
		}
	}

	// Flush the last criterion.
	flushCriterion()

	return criteria, nil
}

// buildTSCControlTree converts parsed criteria into the TreeResult.
func buildTSCControlTree(criteria []tscParsedCriterion, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "TSC " + versionLabel,
	}

	var ordinal int32
	for _, crit := range criteria {
		// Criterion row.
		critIdx := len(result.Controls)

		body := crit.Body
		var bodyPtr *string
		if body != "" {
			bodyPtr = &body
		}

		cr := ControlRow{
			Citation:      crit.Citation,
			CitationNorm:  strings.ToUpper(crit.Citation),
			Kind:          "criterion",
			Status:        "active",
			Title:         "Criterion " + crit.Citation,
			TitleOriginal: nil, // AICPA licensed text; always nil
			Body:          bodyPtr,
			ParentIdx:     -1, // criteria are roots
			Ordinal:       ordinal,
		}
		result.Controls = append(result.Controls, cr)
		ordinal++

		// PoF children.
		for i, pof := range crit.PoFs {
			pofNum := i + 1
			citation := fmt.Sprintf("%s-pof-%02d", crit.Citation, pofNum)
			title := fmt.Sprintf("%s point of focus %d", crit.Citation, pofNum)

			var titleOriginal *string
			if pof.LeadIn != "" {
				titleOriginal = &pof.LeadIn
			}

			pofBody := pof.Body
			var pofBodyPtr *string
			if pofBody != "" {
				pofBodyPtr = &pofBody
			}

			pofRow := ControlRow{
				Citation:      citation,
				CitationNorm:  strings.ToUpper(citation),
				Kind:          "point-of-focus",
				Status:        "active",
				Title:         title,
				TitleOriginal: titleOriginal,
				Body:          pofBodyPtr,
				ParentIdx:     critIdx,
				Ordinal:       ordinal,
			}
			result.Controls = append(result.Controls, pofRow)
			ordinal++
		}
	}

	return result, nil
}
