package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// cobitCapture mirrors extract.PDFCapture for parsing pdf-pages-json.
type cobitCapture struct {
	Pages []cobitPage `json:"pages"`
}

type cobitPage struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// cobitObjective is an intermediate result from parsing an objective header.
type cobitObjective struct {
	Code       string // EDM01, APO02, BAI03, etc.
	DomainCode string // EDM, APO, BAI, DSS, MEA
	Page       int    // source page number
}

// cobitPractice is an intermediate result from parsing a practice line.
type cobitPractice struct {
	Citation      string // EDM01.01, APO02.05, etc.
	ObjectiveCode string // EDM01, APO02, etc.
	DomainCode    string // EDM, APO, BAI, DSS, MEA
	Body          string // description text (everything after the ID up to next practice/section)
}

// reObjectiveHeader matches objective header lines in go-fitz capture.
// Pattern: "Governance Objective:  EDM01" or "Management Objective:  APO01"
// with case-insensitive "Objective" (BAI11 uses lowercase "objective").
var reObjectiveHeader = regexp.MustCompile(
	`(?i)(?:Governance|Management)\s+Objective:\s+((?:EDM|APO|BAI|DSS|MEA)\d{2})\s`)

// rePracticeID matches COBIT practice IDs at line start: EDM01.01, APO14.10, etc.
var rePracticeID = regexp.MustCompile(
	`^((?:EDM|APO|BAI|DSS|MEA)\d{2}\.\d{2})\s`)

// reActivityLine matches numbered activity lines (1., 2., etc.) that should
// be excluded from practice body text.
var reActivityLine = regexp.MustCompile(`^\d+\.\s`)

// reSectionBreak matches structural section headings that signal the end of
// practice body text within the "A. Component: Process" section.
var reSectionBreak = regexp.MustCompile(
	`^(Activities\s|Related Guidance|B\.\s+Component|C\.\s+Component|D\.\s+Component|E\.\s+Component|F\.\s+Component|G\.\s+Component)`)

// reCapabilityLevel matches standalone capability level numbers (2, 3, 4, 5)
// that appear as column artifacts in the practices table.
var reCapabilityLevel = regexp.MustCompile(`^\d$`)

// reExampleMetric matches "a. ...", "b. ..." metric lines.
var reExampleMetric = regexp.MustCompile(`^[a-z]\.\s`)

// cobitDomainNames maps domain codes to their full names.
var cobitDomainNames = map[string]string{
	"EDM": "Evaluate, Direct and Monitor",
	"APO": "Align, Plan and Organize",
	"BAI": "Build, Acquire and Implement",
	"DSS": "Deliver, Service and Support",
	"MEA": "Monitor, Evaluate and Assess",
}

// BuildCOBITTree parses a pdf-pages-json capture for COBIT 2019 and returns
// the normalized control tree. This is a pure function with no side effects.
//
// Tree shape per design decision 4:
// 5 domain roots (kind 'domain', citations EDM/APO/BAI/DSS/MEA)
//
//	-> 40 objectives (kind 'objective')
//	  -> practices (kind 'practice')
//
// Title = neutral generated label ("Practice EDM01.01", "Objective EDM01");
// title_original = nil (licensed titling — ISACA text never in public-safe fields).
// Body = practice description text from the PDF (auth-gated in bronze).
func BuildCOBITTree(raw json.RawMessage, _, versionLabel string) (*TreeResult, error) {
	var cap cobitCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	// Pass 1: find all objective headers and their page numbers.
	objectives, err := findCOBITObjectives(cap.Pages)
	if err != nil {
		return nil, err
	}

	if len(objectives) == 0 {
		return nil, fmt.Errorf("no objective headers found in capture")
	}

	// Pass 2: for each objective, find practices within its page range.
	practices := findCOBITPractices(cap.Pages, objectives)

	// Pass 3: build the tree.
	return buildCOBITControlTree(objectives, practices, versionLabel)
}

// findCOBITObjectives scans all pages for objective header lines.
// Returns objectives in document order, deduplicated.
func findCOBITObjectives(pages []cobitPage) ([]cobitObjective, error) {
	seen := make(map[string]bool)
	var objectives []cobitObjective

	for _, pg := range pages {
		for _, line := range strings.Split(pg.Text, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if m := reObjectiveHeader.FindStringSubmatch(trimmed); m != nil {
				code := m[1]
				if !seen[code] {
					seen[code] = true
					objectives = append(objectives, cobitObjective{
						Code:       code,
						DomainCode: code[:3],
						Page:       pg.N,
					})
				}
			}
		}
	}

	return objectives, nil
}

// findCOBITPractices scans pages for practice IDs, using objective page
// ranges to determine which practices belong to which objective.
// Only the first occurrence of each practice ID within its parent
// objective's section is kept (duplicates in RACI tables, info-flow
// tables, and overview sections are skipped).
//
// All pages within an objective's range are flattened into one line slice
// so that extractPracticeBody can continue collecting body text across PDF
// page boundaries.
func findCOBITPractices(pages []cobitPage, objectives []cobitObjective) []cobitPractice {
	// Build page-range lookup: for each objective, its section runs from
	// its start page to the next objective's start page (exclusive).
	type pageRange struct {
		obj   cobitObjective
		start int
		end   int // exclusive; 999999 for last
	}
	var ranges []pageRange
	for i, obj := range objectives {
		end := 999999
		if i+1 < len(objectives) {
			end = objectives[i+1].Page
		}
		ranges = append(ranges, pageRange{obj: obj, start: obj.Page, end: end})
	}

	// For each objective range, flatten all relevant pages into one line
	// slice, then scan for practice IDs. This lets extractPracticeBody
	// continue collecting body text past a page boundary.
	seen := make(map[string]bool)
	var practices []cobitPractice

	for _, pr := range ranges {
		// Collect all lines from pages within this objective's range.
		var allLines []string
		for _, pg := range pages {
			if pg.N >= pr.start && pg.N < pr.end {
				allLines = append(allLines, strings.Split(pg.Text, "\n")...)
			}
		}

		for i, line := range allLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			m := rePracticeID.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}

			id := m[1]
			objCode := id[:5]

			// Only accept practices belonging to the current objective.
			if objCode != pr.obj.Code {
				continue
			}

			// Skip duplicates.
			if seen[id] {
				continue
			}
			seen[id] = true

			// Extract body: text after the practice ID on this line,
			// plus continuation lines until a boundary (crosses page
			// boundaries since allLines spans the full objective range).
			body := extractPracticeBody(trimmed, id, allLines, i)

			practices = append(practices, cobitPractice{
				Citation:      id,
				ObjectiveCode: objCode,
				DomainCode:    objCode[:3],
				Body:          body,
			})
		}
	}

	return practices
}

// extractPracticeBody extracts the description text for a practice.
// It takes the text after the ID on the current line, then continues
// collecting text from subsequent lines until hitting a boundary
// (next practice ID, Activities section, section break, etc.).
func extractPracticeBody(currentLine, id string, allLines []string, lineIdx int) string {
	var parts []string

	// Text after the practice ID on the current line.
	rest := strings.TrimSpace(currentLine[len(id):])
	if rest != "" {
		parts = append(parts, rest)
	}

	// Continue with subsequent lines.
	for j := lineIdx + 1; j < len(allLines); j++ {
		next := strings.TrimSpace(allLines[j])
		if next == "" {
			continue
		}

		// Stop at boundaries.
		if rePracticeID.MatchString(next) {
			break
		}
		if reSectionBreak.MatchString(next) {
			break
		}
		if reActivityLine.MatchString(next) {
			break
		}
		if reCapabilityLevel.MatchString(next) {
			continue // skip standalone capability-level numbers
		}
		if reExampleMetric.MatchString(next) {
			continue // skip "a. ..." metric lines
		}
		// Skip page-number-only lines.
		if isPageNumber(next) {
			continue
		}
		// Skip structural noise.
		if isCOBITSkipLine(next) {
			continue
		}

		parts = append(parts, next)
	}

	return strings.Join(parts, "\n")
}

// isPageNumber returns true if the line is just a page number.
func isPageNumber(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0 && len(s) <= 4
}

// isCOBITSkipLine returns true for structural lines that are not practice
// body text (section headers, component markers, framework title lines).
func isCOBITSkipLine(line string) bool {
	skip := []string{
		"Governance Practice",
		"Management Practice",
		"Example Metrics",
		"Key Governance Practice",
		"Key Management Practice",
		"A. Component: Process",
		"Component: Process",
	}
	for _, s := range skip {
		if strings.HasPrefix(line, s) {
			return true
		}
	}
	// Chapter headers and framework title lines.
	if strings.HasPrefix(line, "CHAPTER ") {
		return true
	}
	if strings.Contains(line, "COBIT") && strings.Contains(line, "FRAMEWORK") {
		return true
	}
	return false
}

// buildCOBITControlTree constructs the TreeResult from parsed objectives
// and practices.
func buildCOBITControlTree(
	objectives []cobitObjective,
	practices []cobitPractice,
	versionLabel string,
) (*TreeResult, error) {
	result := &TreeResult{
		Title: "COBIT " + versionLabel,
	}

	citationToIdx := make(map[string]int)
	var ordinal int32

	// Track which domains we have emitted.
	domainEmitted := make(map[string]bool)

	// Build practice lookup: objective code -> practices.
	pracByObj := make(map[string][]cobitPractice)
	for _, p := range practices {
		pracByObj[p.ObjectiveCode] = append(pracByObj[p.ObjectiveCode], p)
	}

	for _, obj := range objectives {
		// Emit domain root if not yet emitted.
		if !domainEmitted[obj.DomainCode] {
			domainEmitted[obj.DomainCode] = true
			domainName, ok := cobitDomainNames[obj.DomainCode]
			if !ok {
				domainName = obj.DomainCode
			}
			cr := ControlRow{
				Citation:     obj.DomainCode,
				CitationNorm: obj.DomainCode,
				Kind:         "domain",
				Status:       "active",
				Title:        domainName,
				ParentIdx:    -1,
				Ordinal:      ordinal,
			}
			citationToIdx[obj.DomainCode] = len(result.Controls)
			result.Controls = append(result.Controls, cr)
			ordinal++
		}

		// Emit objective.
		domainIdx := citationToIdx[obj.DomainCode]
		cr := ControlRow{
			Citation:     obj.Code,
			CitationNorm: obj.Code,
			Kind:         "objective",
			Status:       "active",
			Title:        "Objective " + obj.Code,
			ParentIdx:    domainIdx,
			Ordinal:      ordinal,
		}
		citationToIdx[obj.Code] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++

		// Emit practices for this objective.
		objIdx := citationToIdx[obj.Code]
		for _, prac := range pracByObj[obj.Code] {
			var body *string
			if prac.Body != "" {
				b := prac.Body
				body = &b
			}
			cr := ControlRow{
				Citation:     prac.Citation,
				CitationNorm: prac.Citation,
				Kind:         "practice",
				Status:       "active",
				Title:        "Practice " + prac.Citation,
				Body:         body,
				ParentIdx:    objIdx,
				Ordinal:      ordinal,
			}
			result.Controls = append(result.Controls, cr)
			ordinal++
		}
	}

	return result, nil
}
