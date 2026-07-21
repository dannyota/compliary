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

// rePCIMidLineID matches a requirement ID appearing mid-line (after other text).
// go-fitz sometimes concatenates text from adjacent PDF columns on a single line,
// causing a requirement ID to appear mid-line rather than at position 0.
// Requires a space before the ID.
var rePCIMidLineID = regexp.MustCompile(`\s(A?\d+\.\d+(?:\.\d+)+)\s`)

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
		if strings.EqualFold(strings.TrimSpace(line), label+":") {
			return true
		}
	}
	return false
}

// rePCISkipLine matches structural headers from the requirement column itself
// ("Defined Approach Requirements" / "Requirements and Testing Procedures")
// that should be skipped (they label our column, not content).
var rePCISkipLine = regexp.MustCompile(`^(Defined Approach Requirements|Requirements and Testing Procedures)\s*$`)

// rePCIStopLine matches lines containing headers that begin the Testing
// Procedures or Guidance columns. go-fitz often concatenates multiple column
// headers onto a single line (e.g. "Requirements and Testing Procedures
// Guidance"), so the match uses substring checks for known column-header
// combos. The "Guidance" branch is anchored to the full line (^Guidance$)
// to avoid truncating body text that merely mentions the word.
// Everything from the first stop-line onward is non-requirement content.
var rePCIStopLine = regexp.MustCompile(
	`Defined Approach Testing Procedures|Requirements and Testing Procedures|^Guidance\s*$`)

// BuildPCITree parses a pdf-pages-json capture for PCI DSS and returns the
// normalized control tree. This is a pure function with no side effects.
//
// Tree shape per design decision 3: kind 'requirement' at every level.
// Roots: citations 1-12 (from "Requirement N:" headers) plus A1, A2, A3
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
//  2. Defined Approach Requirement (the normative text - what we want)
//  3. Testing Procedure description (same ID, different text)
//
// go-fitz sometimes concatenates text from adjacent PDF columns onto a single
// line. This causes a requirement ID to appear mid-line rather than at line
// start. The parser detects this by doing a pre-scan to inventory all IDs at
// line-start, then splitting lines that contain IDs not in that inventory.
func parsePCIPages(pages []pciPage) ([]pciParsedItem, error) {
	// Pre-scan: build inventory of all IDs that appear at line-start.
	// Any ID NOT in this set that appears mid-line is a candidate for
	// line-splitting (it was missed because go-fitz concatenated columns).
	lineStartIDs := make(map[string]bool)
	for _, page := range pages {
		for _, line := range strings.Split(page.Text, "\n") {
			stripped := strings.TrimSpace(line)
			if stripped == "" {
				continue
			}
			if rePCITestProc.MatchString(stripped) {
				continue
			}
			if rePCIContinued.MatchString(stripped) {
				continue
			}
			if m := rePCIReqID.FindStringSubmatch(stripped); m != nil {
				lineStartIDs[m[1]] = true
			}
		}
	}

	// Classify every line, splitting mid-line IDs that are NOT in the
	// line-start inventory. This handles the go-fitz column concatenation
	// case where a requirement ID appears mid-line because the preceding
	// guidance/testing text didn't end with a newline.
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

			if stripped == "" {
				allLines = append(allLines, lineInfo{page: page.N, text: ""})
				continue
			}

			// Check for mid-line IDs that are NOT in the line-start inventory.
			// Only split for IDs that:
			// 1. Do NOT appear at line-start anywhere (missed by go-fitz)
			// 2. Have at least one sibling in the line-start inventory
			//    (same parent, different last segment) — this filters out
			//    external standard references and version numbers
			split := false
			if loc := rePCIMidLineID.FindStringIndex(stripped); loc != nil {
				candidate := rePCIMidLineID.FindStringSubmatch(stripped)[1]
				if loc[0] > 0 && !lineStartIDs[candidate] &&
					hasSiblingInSet(candidate, lineStartIDs) &&
					!isInsideBrackets(stripped, loc[0]) {
					// This ID never appears at line-start, has siblings,
					// and is not inside brackets (external standard ref) —
					// it's a missed requirement due to column concatenation.
					before := strings.TrimSpace(stripped[:loc[0]])
					after := strings.TrimSpace(stripped[loc[0]+1:]) // +1 to skip space
					if before != "" {
						allLines = append(allLines, classifyLine(page.N, before))
					}
					allLines = append(allLines, classifyLine(page.N, after))
					split = true
				}
			}

			if !split {
				allLines = append(allLines, classifyLine(page.N, stripped))
			}
		}
	}

	// Pass 1: identify which citation IDs are "new" (first occurrence)
	// and record their line positions.
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

	// Pass 2: for each first-seen item, collect its body.
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

		// Collect remaining lines until the next first-seen item or a
		// column-boundary stop line. The PDF's 3-column table linearizes as
		// requirement → testing procedures → guidance; the stop line marks
		// where the requirement column ends and the noise begins.
	bodyLoop:
		for j := fs.lineIdx + 1; j < endIdx; j++ {
			nxt := allLines[j]

			switch nxt.lineType {
			case "header", "id", "testproc", "continued":
				continue
			default:
				text := nxt.text

				if rePCIStopLine.MatchString(text) {
					break bodyLoop
				}
				if rePCISkipLine.MatchString(text) {
					continue
				}

				if isPCIBodyLabel(text) {
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

// classifyLine determines the type of a text line.
func classifyLine(page int, text string) struct {
	page     int
	text     string
	lineType string
	citation string
} {
	type lineInfo struct {
		page     int
		text     string
		lineType string
		citation string
	}

	li := lineInfo{page: page, text: text}

	if m := reReqHeader.FindStringSubmatch(text); m != nil {
		li.lineType = "header"
		li.citation = m[1]
	} else if m := reAppHeader.FindStringSubmatch(text); m != nil {
		li.lineType = "header"
		li.citation = m[1]
	} else if m := rePCIContinued.FindStringSubmatch(text); m != nil {
		li.lineType = "continued"
		li.citation = m[1]
	} else if m := rePCITestProc.FindStringSubmatch(text); m != nil {
		li.lineType = "testproc"
		li.citation = m[1]
	} else if m := rePCIReqID.FindStringSubmatch(text); m != nil {
		li.lineType = "id"
		li.citation = m[1]
	}

	return struct {
		page     int
		text     string
		lineType string
		citation string
	}(li)
}

// buildPCIControlTree converts parsed items into the TreeResult.
func buildPCIControlTree(items []pciParsedItem, _, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "PCI DSS " + versionLabel,
	}

	// Track citation -> index for parent resolution.
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
			TitleOriginal: nil,
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

// isInsideBrackets checks whether position pos in text falls between
// an opening '[' and closing ']'. Used to filter out external standard
// references that appear inside bracketed text.
func isInsideBrackets(text string, pos int) bool {
	// Scan backward from pos for '[' without hitting ']'.
	for i := pos - 1; i >= 0; i-- {
		if text[i] == '[' {
			return true
		}
		if text[i] == ']' {
			return false
		}
	}
	return false
}

// hasSiblingInSet checks whether a citation has at least one sibling (same
// parent, different last segment) in the provided set. This is used to
// validate that a mid-line ID is a real requirement and not an external
// standard reference or version number.
// "10.2.1.4" siblings: "10.2.1.3", "10.2.1.5", etc.
func hasSiblingInSet(citation string, ids map[string]bool) bool {
	parent := pciParentCitation(citation)
	if parent == "" {
		return false
	}
	prefix := parent + "."
	for id := range ids {
		if id != citation && strings.HasPrefix(id, prefix) {
			// Verify it's a direct child (no additional dots after prefix).
			rest := id[len(prefix):]
			if !strings.Contains(rest, ".") {
				return true
			}
		}
	}
	return false
}

// pciParentCitation returns the parent citation for a PCI DSS citation.
func pciParentCitation(citation string) string {
	lastDot := strings.LastIndex(citation, ".")
	if lastDot < 0 {
		return ""
	}
	return citation[:lastDot]
}
