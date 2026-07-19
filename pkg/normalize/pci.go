package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// pciCapture mirrors extract.PDFCapture for parsing pdf-pages-json.
type pciCapture struct {
	Pages []pciPage `json:"pages"`
}

type pciPage struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// pciParsedItem is an intermediate result from parsing the capture text.
type pciParsedItem struct {
	Citation string // "1", "1.1", "A1", "A1.1.1", etc.
	Kind     string // "header" (Requirement N / Appendix AN) or "id" (numbered item)
	Page     int    // source page number
	Body     string // body text (Defined Approach Requirement + labeled sections)
}

// reReqHeader matches "Requirement N: ..." at line start.
var reReqHeader = regexp.MustCompile(`^Requirement\s+(\d+):`)

// reAppHeader matches "Appendix AN: ..." at line start.
var reAppHeader = regexp.MustCompile(`^Appendix\s+(A\d+):`)

// rePCIReqID matches numbered requirement IDs at line start.
// Handles regular (1.1, 1.1.1, 1.1.1.1) and appendix (A1.1, A1.1.1, A3.2.5.1) forms.
var rePCIReqID = regexp.MustCompile(`^(A?\d+(?:\.\d+)+)\s`)

// rePCITestProc matches testing procedure lines (.a, .b, .a.1, etc.).
var rePCITestProc = regexp.MustCompile(`^(A?\d+(?:\.\d+)+\.[a-z](?:\.\d+)?)\s`)

// rePCIContinued detects "(continued)" lines that continue a prior item.
var rePCIContinued = regexp.MustCompile(`^(A?\d+(?:\.\d+)+)\s+\(continued\)`)

// Structural labels to include in body when they appear as standalone lines.
var pciBodyLabels = []string{
	"Customized Approach Objective",
	"Applicability Notes",
}

// isPCIBodyLabel checks if a line is a structural label that should be
// included in the body text.
func isPCIBodyLabel(line string) bool {
	for _, label := range pciBodyLabels {
		if strings.EqualFold(strings.TrimSpace(line), label) {
			return true
		}
		// Also match with trailing colon.
		if strings.EqualFold(strings.TrimSpace(line), label+":") {
			return true
		}
	}
	return false
}

// isPCISkipLine checks if a line should be excluded from body text.
// These are structural artifacts from the 3-column layout that aren't
// part of the Defined Approach Requirement content.
var rePCISkipLine = regexp.MustCompile(`^(Defined Approach (Requirements|Testing Procedures)|Requirements and Testing Procedures|Good Practice|Purpose|Definitions|Examples|Further Information|Guidance)\s*$`)

// BuildPCITree parses a pdf-pages-json capture for PCI DSS and returns the
// normalized control tree. This is a pure function with no side effects.
//
// Tree shape per design decision 3: kind 'requirement' at every level.
// Roots: citations 1–12 (from "Requirement N:" headers) plus A1, A2, A3
// (from "Appendix AN:" headers). Children by numeric nesting.
// Title = generated "Requirement <citation>" (no licensed text).
// title_original = nil. Body = Defined Approach Requirement text with
// Customized Approach Objective and Applicability Notes sections; testing
// procedures and guidance are DEFERRED.
func BuildPCITree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var cap pciCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	// Parse all pages into ordered items, deduplicated.
	items, err := parsePCIPages(cap.Pages)
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no requirement headers or IDs found in capture")
	}

	// Build control tree from items.
	return buildPCIControlTree(items, frameworkCode, versionLabel)
}

// parsePCIPages scans all pages for requirement headers and numbered IDs,
// collecting body text. Testing procedure lines and duplicate ID occurrences
// (summary listings, testing procedure descriptions) are excluded.
//
// The PCI DSS document repeats IDs in three contexts:
//  1. Summary listing on the requirement section's intro page
//  2. Defined Approach Requirement (the normative text — what we want)
//  3. Testing Procedure description (same ID, different text)
//
// The 3-column layout (Requirements | Testing Procedures | Guidance) is
// extracted by go-fitz as sequential text. The requirement text, its labeled
// sections (Customized Approach Objective, Applicability Notes), the testing
// procedure text, and guidance text all appear as consecutive lines.
//
// Strategy: keep the first occurrence of each citation ID. Its body is the
// text between the first-line and the next NEW (first-seen) structural item,
// filtered to keep only lines that are:
//   - Plain text (not a duplicate ID or testing procedure letter)
//   - Structural labels (CAO, Applicability Notes)
//
// Lines that are duplicate IDs, testing procedures, or guidance-column
// artifacts are filtered out.
func parsePCIPages(pages []pciPage) ([]pciParsedItem, error) {
	// Classify every line.
	type lineInfo struct {
		page     int
		text     string
		lineType string // "header", "id", "testproc", "continued", ""
		citation string
	}

	var allLines []lineInfo
	for _, page := range pages {
		for _, line := range strings.Split(page.Text, "\n") {
			stripped := strings.TrimSpace(line)
			li := lineInfo{page: page.N, text: stripped}

			if stripped == "" {
				allLines = append(allLines, li)
				continue
			}

			if m := reReqHeader.FindStringSubmatch(stripped); m != nil {
				li.lineType = "header"
				li.citation = m[1]
			} else if m := reAppHeader.FindStringSubmatch(stripped); m != nil {
				li.lineType = "header"
				li.citation = m[1]
			} else if m := rePCIContinued.FindStringSubmatch(stripped); m != nil {
				li.lineType = "continued"
				li.citation = m[1]
			} else if m := rePCITestProc.FindStringSubmatch(stripped); m != nil {
				li.lineType = "testproc"
				li.citation = m[1]
			} else if m := rePCIReqID.FindStringSubmatch(stripped); m != nil {
				li.lineType = "id"
				li.citation = m[1]
			}

			allLines = append(allLines, li)
		}
	}

	// Pass 1: identify which citation IDs are "new" (first occurrence)
	// and record their line positions. Build a set of all first-seen positions.
	seen := make(map[string]bool)
	type itemStart struct {
		lineIdx  int
		citation string
		kind     string // "header" or "id"
		page     int
	}
	var firstSeen []itemStart

	for i, li := range allLines {
		if li.lineType == "header" || li.lineType == "id" {
			if !seen[li.citation] {
				seen[li.citation] = true
				firstSeen = append(firstSeen, itemStart{
					lineIdx:  i,
					citation: li.citation,
					kind:     li.lineType,
					page:     li.page,
				})
			}
		}
	}

	// Pass 2: for each first-seen item, collect its body. The body extends
	// from the line after the item until the next first-seen item. Within
	// that range, filter out:
	// - Lines matching duplicate IDs (already-seen headers/IDs)
	// - Testing procedure lines
	// - "(continued)" markers for this item (handle separately)
	// - Lines that are structural skip patterns (column headers, guidance labels)
	// Include:
	// - Plain text lines
	// - Structural body labels (Customized Approach Objective, Applicability Notes)

	// Build a set of first-seen line indices for quick boundary lookup.
	firstSeenSet := make(map[int]bool, len(firstSeen))
	for _, fs := range firstSeen {
		firstSeenSet[fs.lineIdx] = true
	}

	var items []pciParsedItem
	for fsIdx, fs := range firstSeen {
		// Find end boundary: next first-seen item.
		endIdx := len(allLines)
		if fsIdx+1 < len(firstSeen) {
			endIdx = firstSeen[fsIdx+1].lineIdx
		}

		// Collect body lines.
		var bodyParts []string

		// For ID items, include text after the citation on the first line.
		if fs.kind == "id" {
			itemLine := allLines[fs.lineIdx].text
			if len(itemLine) > len(fs.citation) {
				rest := strings.TrimSpace(itemLine[len(fs.citation):])
				if rest != "" {
					bodyParts = append(bodyParts, rest)
				}
			}
		}

		// Collect remaining lines until the next first-seen item.
		inBody := true
		for j := fs.lineIdx + 1; j < endIdx && inBody; j++ {
			nxt := allLines[j]

			switch nxt.lineType {
			case "header":
				// Should not happen (end boundary is next first-seen).
				continue
			case "id":
				// Duplicate ID — skip its text but continue scanning.
				continue
			case "testproc":
				// Testing procedure — skip.
				continue
			case "continued":
				// "(continued)" for this citation — skip the marker, body
				// lines follow and will be collected as plain text.
				continue
			default:
				// Plain text line.
				text := nxt.text

				// Skip structural column-header artifacts.
				if rePCISkipLine.MatchString(text) {
					continue
				}

				// Include body labels and regular text.
				if isPCIBodyLabel(text) {
					// Add as "Label:" format.
					bodyParts = append(bodyParts, text+":")
				} else {
					bodyParts = append(bodyParts, text)
				}
			}
		}

		// Trim trailing empty lines.
		for len(bodyParts) > 0 && bodyParts[len(bodyParts)-1] == "" {
			bodyParts = bodyParts[:len(bodyParts)-1]
		}

		body := strings.Join(bodyParts, "\n")

		items = append(items, pciParsedItem{
			Citation: fs.citation,
			Kind:     fs.kind,
			Page:     fs.page,
			Body:     body,
		})
	}

	return items, nil
}

// buildPCIBody assembles the body text from the first-line remainder and
// subsequent lines. Trims trailing empty lines.
func buildPCIBody(firstLine string, lines []string) string {
	var parts []string
	if firstLine != "" {
		parts = append(parts, firstLine)
	}
	parts = append(parts, lines...)

	// Trim trailing empty entries.
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}

	return strings.Join(parts, "\n")
}

// buildPCIControlTree converts parsed items into the TreeResult.
func buildPCIControlTree(items []pciParsedItem, _, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "PCI DSS " + versionLabel,
	}

	// Track citation → index for parent resolution.
	citationToIdx := make(map[string]int)

	var ordinal int32
	for _, item := range items {
		citation := item.Citation

		cr := ControlRow{
			Citation:      citation,
			CitationNorm:  strings.ToUpper(citation),
			Kind:          "requirement",
			Status:        "active",
			Title:         "Requirement " + citation,
			TitleOriginal: nil, // licensed framework, no original title
			Ordinal:       ordinal,
		}

		// Determine parent.
		parentCite := pciParentCitation(citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		// Body: root nodes (headers) get no body; ID items get their parsed body.
		if item.Kind == "id" && item.Body != "" {
			body := item.Body
			cr.Body = &body
		}

		citationToIdx[citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

// pciParentCitation returns the parent citation for a PCI DSS citation.
// "1.1" → "1", "1.1.1" → "1.1", "A1.1.1" → "A1.1", "A1.1" → "A1",
// "1" → "", "A1" → "".
func pciParentCitation(citation string) string {
	lastDot := strings.LastIndex(citation, ".")
	if lastDot < 0 {
		return ""
	}
	return citation[:lastDot]
}
