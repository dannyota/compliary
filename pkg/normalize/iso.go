package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// isoCapture mirrors extract.PDFCapture for parsing pdf-pages-json.
type isoCapture struct {
	Pages []isoPage `json:"pages"`
}

type isoPage struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// isoParsedItem is an intermediate result from parsing an ISO capture.
type isoParsedItem struct {
	Citation string // e.g. "4.1", "A.5.1", "5.1", "CLD.6.3.1"
	Kind     string // "clause", "domain", "annex-control", "control"
	Page     int
	Body     string
}

// --- Regex patterns ---

// reISOClause matches clause-level headings (4, 5, ..., 10) at line start.
var reISOClause = regexp.MustCompile(`^(\d+)\s+(.+)`)

// reISOSubClause matches subclauses (4.1, 6.1.3, etc.) at line start.
var reISOSubClause = regexp.MustCompile(`^(\d+(?:\.\d+)+)\s+(.+)`)

// reISOAnnexControl matches Annex A controls in 27001 table (N.M at line start
// on annex pages, where they lack the "A." prefix).
var reISO27001AnnexControl = regexp.MustCompile(`^(\d+\.\d+)\s+(.+)`)

// reISO27018Annex matches Annex A controls in 27018 (A.N.M at line start).
var reISO27018Annex = regexp.MustCompile(`^(A\.\d+\.\d+)\s`)

// reISOCLD matches CLD.x.y[.z] cloud control IDs in 27017.
var reISOCLD = regexp.MustCompile(`^(CLD\.\d+\.\d+(?:\.\d+)?)\s`)

// reISOBarePageNum matches bare page numbers (digits only) for skip-line filtering.
var reISOBarePageNum = regexp.MustCompile(`^\d+$`)

// --- 27001 (iso-ams scheme) ---

// BuildISO27001Tree parses a pdf-pages-json capture for ISO/IEC 27001:2022
// and returns the normalized control tree. Pure function, no side effects.
//
// Tree shape: clause tree (kind 'clause', clauses 4-10 with subclauses from
// the normative body) + Annex A rows (kind 'annex-control', citation A.x.y,
// parented to A-theme domain rows A.5-A.8). The Annex A table renders
// controls as "N.M <title> Control" without the A. prefix; the parser
// identifies the table region and prefixes "A." to form the citation.
//
// Title = neutral generated label (no licensed text in public-safe fields).
// title_original = nil.
func BuildISO27001Tree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var cap isoCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	if len(cap.Pages) == 0 {
		return nil, fmt.Errorf("no pages in capture")
	}

	// TODO: parameterize the title prefix when 27701/22301/42001 land on iso-ams.
	result := &TreeResult{
		Title: "ISO/IEC 27001 " + versionLabel,
	}

	// Phase 1: Parse clause tree from normative body (clauses 4-10).
	clauses := parseISO27001Clauses(cap.Pages)

	// Phase 2: Parse Annex A table.
	annexItems := parseISO27001AnnexA(cap.Pages)

	if len(clauses) == 0 && len(annexItems) == 0 {
		return nil, fmt.Errorf("no clauses or Annex A controls found in capture")
	}

	// Build the tree: clauses first, then Annex A.
	citationToIdx := make(map[string]int)
	var ordinal int32

	// Insert clauses.
	for _, item := range clauses {
		cr := ControlRow{
			Citation:     item.Citation,
			CitationNorm: strings.ToUpper(item.Citation),
			Kind:         "clause",
			Status:       "active",
			Title:        "Clause " + item.Citation,
			Ordinal:      ordinal,
		}

		// Parent resolution: 4.1 -> 4, 6.1.3 -> 6.1.
		parentCite := isoParentCitation(item.Citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if item.Body != "" {
			body := item.Body
			cr.Body = &body
		}

		citationToIdx[item.Citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	// Insert Annex A domain rows (A.5-A.8) and controls.
	// First, collect domain IDs from annex items.
	domainSet := make(map[string]bool)
	for _, item := range annexItems {
		if item.Kind == "domain" {
			domainSet[item.Citation] = true
		}
	}

	for _, item := range annexItems {
		cr := ControlRow{
			Citation:     item.Citation,
			CitationNorm: strings.ToUpper(item.Citation),
			Kind:         item.Kind,
			Status:       "active",
			Ordinal:      ordinal,
		}

		switch item.Kind {
		case "domain":
			cr.Title = "Annex A theme " + item.Citation
		case "annex-control":
			cr.Title = "Annex A control " + item.Citation
		}

		// Parent: A.5.1 -> A.5, A.5 -> (root).
		parentCite := isoParentCitation(item.Citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if item.Body != "" {
			body := item.Body
			cr.Body = &body
		}

		citationToIdx[item.Citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

// parseISO27001Clauses extracts clauses 4-10 from the normative body.
func parseISO27001Clauses(pages []isoPage) []isoParsedItem {
	// Find the Annex A boundary (first page containing "Table A.1" as a
	// table continuation header, which appears on the Annex A content pages).
	annexStartPage := -1
	for _, p := range pages {
		lines := strings.Split(p.Text, "\n")
		for _, line := range lines {
			stripped := strings.TrimSpace(line)
			if strings.HasPrefix(stripped, "Table A.1") {
				// Only count pages where Table A.1 appears as content
				// (not just in table of contents references).
				if p.N > 10 { // ToC is typically in first few pages
					if annexStartPage < 0 || p.N < annexStartPage {
						annexStartPage = p.N
					}
				}
			}
		}
	}

	// If no annex boundary found, include all pages.
	maxPage := 99999
	if annexStartPage > 0 {
		maxPage = annexStartPage
	}

	type lineInfo struct {
		page int
		text string
	}

	var allLines []lineInfo
	for _, p := range pages {
		if p.N >= maxPage {
			break
		}
		for _, line := range strings.Split(p.Text, "\n") {
			allLines = append(allLines, lineInfo{page: p.N, text: strings.TrimSpace(line)})
		}
	}

	// Scan for clause/subclause headings in range 4-10.
	type clauseStart struct {
		lineIdx  int
		citation string
		page     int
	}

	var starts []clauseStart
	seen := make(map[string]bool)

	for i, li := range allLines {
		if li.text == "" {
			continue
		}

		// Skip structural lines.
		if isISO27001SkipLine(li.text) {
			continue
		}

		// Try subclause first (more specific).
		if m := reISOSubClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			mainClause := strings.Split(num, ".")[0]
			n, err := strconv.Atoi(mainClause)
			if err == nil && n >= 4 && n <= 10 && !seen[num] {
				seen[num] = true
				starts = append(starts, clauseStart{lineIdx: i, citation: num, page: li.page})
			}
		} else if m := reISOClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			n, err := strconv.Atoi(num)
			if err == nil && n >= 4 && n <= 10 && !seen[num] {
				seen[num] = true
				starts = append(starts, clauseStart{lineIdx: i, citation: num, page: li.page})
			}
		}
	}

	// Collect body for each clause.
	var items []isoParsedItem
	for idx, cs := range starts {
		endLine := len(allLines)
		if idx+1 < len(starts) {
			endLine = starts[idx+1].lineIdx
		}

		var bodyParts []string
		// Skip the heading line itself, collect until next clause.
		for j := cs.lineIdx + 1; j < endLine; j++ {
			text := allLines[j].text
			if text == "" {
				continue
			}
			if isISO27001SkipLine(text) {
				continue
			}
			bodyParts = append(bodyParts, text)
		}

		body := strings.Join(bodyParts, "\n")
		body = strings.TrimSpace(body)

		items = append(items, isoParsedItem{
			Citation: cs.citation,
			Kind:     "clause",
			Page:     cs.page,
			Body:     body,
		})
	}

	return items
}

// parseISO27001AnnexA extracts the 93 Annex A controls from the table.
// The table renders controls as "N.M <title> Control" without the A. prefix.
// Theme headings appear as "N <theme-name>" (5, 6, 7, 8).
func parseISO27001AnnexA(pages []isoPage) []isoParsedItem {
	// Find Annex A table pages (pages containing "Table A.1").
	var annexPages []isoPage
	for _, p := range pages {
		if strings.Contains(p.Text, "Table A.1") && p.N > 10 {
			annexPages = append(annexPages, p)
		}
	}

	if len(annexPages) == 0 {
		return nil
	}

	// Themes in Annex A: 5=Organizational, 6=People, 7=Physical, 8=Technological.
	validThemes := map[string]bool{"5": true, "6": true, "7": true, "8": true}

	type lineInfo struct {
		page int
		text string
	}

	var allLines []lineInfo
	for _, p := range annexPages {
		for _, line := range strings.Split(p.Text, "\n") {
			stripped := strings.TrimSpace(line)
			if stripped == "" {
				continue
			}
			if isISO27001AnnexSkipLine(stripped) {
				continue
			}
			allLines = append(allLines, lineInfo{page: p.N, text: stripped})
		}
	}

	// Parse: theme headings and control entries.
	var items []isoParsedItem
	seen := make(map[string]bool)

	for _, li := range allLines {
		// Check for control (N.M ...).
		if m := reISO27001AnnexControl.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			parts := strings.Split(num, ".")
			if validThemes[parts[0]] && !seen["A."+num] {
				seen["A."+num] = true
				items = append(items, isoParsedItem{
					Citation: "A." + num,
					Kind:     "annex-control",
					Page:     li.page,
					// Body captured from the table line (the normative text).
					Body: extractAnnexBodyAfterID(li.text, num),
				})
			}
			continue
		}

		// Check for theme heading (just a number 5/6/7/8 followed by title).
		if m := reISOClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			if validThemes[num] && !seen["A."+num] {
				seen["A."+num] = true
				items = append(items, isoParsedItem{
					Citation: "A." + num,
					Kind:     "domain",
					Page:     li.page,
				})
			}
		}
	}

	// Sort: domains first by number, then controls in order.
	sort.SliceStable(items, func(i, j int) bool {
		ci := items[i].Citation
		cj := items[j].Citation
		return isoCompare(ci, cj) < 0
	})

	return items
}

// extractAnnexBodyAfterID removes the ID prefix from an Annex A table line.
// E.g. "5.12 Classification of information Control" → the body after the ID.
func extractAnnexBodyAfterID(line, id string) string {
	idx := strings.Index(line, id)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len(id):])
	return rest
}

// isISO27001SkipLine returns true for structural lines that are not clause content.
func isISO27001SkipLine(line string) bool {
	if strings.HasPrefix(line, "ISO/IEC 27001") || strings.HasPrefix(line, "ISO/IEC 2700") {
		return true
	}
	if strings.Contains(line, "All rights reserved") {
		return true
	}
	if strings.HasPrefix(line, "Table A.1") {
		return true
	}
	// Page numbers at the end of lines.
	if reISOBarePageNum.MatchString(line) {
		return true
	}
	// Table-of-contents lines.
	if strings.Contains(line, "...") {
		return true
	}
	return false
}

// isISO27001AnnexSkipLine filters structural artifacts from Annex A pages.
func isISO27001AnnexSkipLine(line string) bool {
	if strings.HasPrefix(line, "ISO/IEC") {
		return true
	}
	if strings.Contains(line, "All rights reserved") {
		return true
	}
	if strings.HasPrefix(line, "Table A.1") {
		return true
	}
	if reISOBarePageNum.MatchString(line) {
		return true
	}
	return false
}

// --- 27002/27017/27018 (iso-control-catalog scheme) ---

// BuildISOControlCatalogTree parses a pdf-pages-json capture for ISO/IEC
// 27002:2022, 27017:2015, or 27018:2019 and returns the normalized control
// tree. Dispatches internally based on frameworkCode.
//
// 27002: theme domains (5-8 kind 'domain') + 93 controls (kind 'control').
// 27017: numbered sections keyed to 27002:2013 (kind 'control') + CLD.*
//
//	cloud controls (kind 'control'). Citations stay publisher-native
//	(27002:2013 numbering, not renumbered).
//
// 27018: numbered sections keyed to 27002:2013 (kind 'control') + Annex A
//
//	PII controls (kind 'annex-control').
func BuildISOControlCatalogTree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var cap isoCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	if len(cap.Pages) == 0 {
		return nil, fmt.Errorf("no pages in capture")
	}

	switch frameworkCode {
	case "iso27002":
		return buildISO27002Tree(cap, versionLabel)
	case "iso27017":
		return buildISO27017Tree(cap, versionLabel)
	case "iso27018":
		return buildISO27018Tree(cap, versionLabel)
	default:
		return nil, fmt.Errorf("unsupported framework for iso-control-catalog: %s", frameworkCode)
	}
}

// buildISO27002Tree parses 27002:2022 — 4 theme domains + 93 controls.
func buildISO27002Tree(cap isoCapture, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "ISO/IEC 27002 " + versionLabel,
	}

	// 27002:2022 uses themes 5-8 as top-level domains, with controls N.M.
	// Parse controls from the capture.
	controls, domains := parseISO27002Controls(cap.Pages)

	if len(controls) == 0 {
		return nil, fmt.Errorf("no controls found in ISO 27002 capture")
	}

	citationToIdx := make(map[string]int)
	var ordinal int32

	// Insert domain rows first.
	for _, d := range domains {
		cr := ControlRow{
			Citation:     d.citation,
			CitationNorm: strings.ToUpper(d.citation),
			Kind:         "domain",
			Status:       "active",
			Title:        "Theme " + d.citation,
			ParentIdx:    -1,
			Ordinal:      ordinal,
		}
		citationToIdx[d.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	// Insert controls.
	for _, c := range controls {
		cr := ControlRow{
			Citation:     c.citation,
			CitationNorm: strings.ToUpper(c.citation),
			Kind:         "control",
			Status:       "active",
			Title:        "Control " + c.citation,
			Ordinal:      ordinal,
		}

		// Parent: N.M -> N (domain).
		parentCite := strings.Split(c.citation, ".")[0]
		if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if c.body != "" {
			// Strip the attribute-table boilerplate that go-fitz leaks into
			// every 27002:2022 control body (Control type / Information
			// security properties / #tags block).
			body := stripISO27002AttributeTable(c.body)
			cr.Body = &body
		}

		citationToIdx[c.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

type isoControlItem struct {
	citation string
	page     int
	body     string
}

// parseISO27002Controls extracts 27002:2022 controls (N.M) and domains (N).
func parseISO27002Controls(pages []isoPage) (controls []isoControlItem, domains []isoControlItem) {
	// Valid themes in 27002:2022.
	validThemes := map[string]bool{"5": true, "6": true, "7": true, "8": true}

	type lineInfo struct {
		page int
		text string
	}

	var allLines []lineInfo
	for _, p := range pages {
		for _, line := range strings.Split(p.Text, "\n") {
			allLines = append(allLines, lineInfo{page: p.N, text: strings.TrimSpace(line)})
		}
	}

	// Find all control headings (N.M) and domain headings (N) for themes 5-8.
	type heading struct {
		lineIdx  int
		citation string
		kind     string // "domain" or "control"
		page     int
	}

	var headings []heading
	seen := make(map[string]bool)

	for i, li := range allLines {
		if li.text == "" {
			continue
		}
		if isISOControlCatalogSkipLine(li.text) {
			continue
		}

		// Try control pattern first (N.M).
		if m := reISOSubClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			parts := strings.Split(num, ".")
			if len(parts) == 2 && validThemes[parts[0]] && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, kind: "control", page: li.page})
			}
			continue
		}

		// Theme heading (single number 5/6/7/8).
		if m := reISOClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			if validThemes[num] && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, kind: "domain", page: li.page})
			}
		}
	}

	// Collect body for each heading.
	for idx, h := range headings {
		endLine := len(allLines)
		if idx+1 < len(headings) {
			endLine = headings[idx+1].lineIdx
		}

		var bodyParts []string
		for j := h.lineIdx + 1; j < endLine; j++ {
			text := allLines[j].text
			if text == "" {
				continue
			}
			if isISOControlCatalogSkipLine(text) {
				continue
			}
			bodyParts = append(bodyParts, text)
		}

		body := strings.Join(bodyParts, "\n")
		body = strings.TrimSpace(body)

		item := isoControlItem{citation: h.citation, page: h.page, body: body}
		if h.kind == "domain" {
			domains = append(domains, item)
		} else {
			controls = append(controls, item)
		}
	}

	return
}

// buildISO27017Tree parses 27017:2015 — 27002:2013-keyed sections + CLD controls.
// Inclusion rule: ALL numbered sections get rows (they are citable); sections
// that merely reference 27002 get a row with that reference body; sections with
// cloud-specific additions get their full guidance body.
func buildISO27017Tree(cap isoCapture, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "ISO/IEC 27017 " + versionLabel,
	}

	// Parse numbered sections (5-18) and CLD entries.
	sections, cldItems := parseISO27017Sections(cap.Pages)

	if len(sections) == 0 {
		return nil, fmt.Errorf("no sections found in ISO 27017 capture")
	}

	citationToIdx := make(map[string]int)
	var ordinal int32

	// Insert numbered sections (all as kind 'control' — they are control-guidance
	// sections keyed to 27002:2013 numbering).
	for _, s := range sections {
		cr := ControlRow{
			Citation:     s.citation,
			CitationNorm: strings.ToUpper(s.citation),
			Kind:         "control",
			Status:       "active",
			Title:        "Control " + s.citation,
			Ordinal:      ordinal,
		}

		// Parent: 5.1.1 -> 5.1 -> 5.
		parentCite := isoParentCitation(s.citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if s.body != "" {
			body := s.body
			cr.Body = &body
		}

		citationToIdx[s.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	// Insert CLD items (cloud extended controls).
	for _, c := range cldItems {
		cr := ControlRow{
			Citation:     c.citation,
			CitationNorm: strings.ToUpper(c.citation),
			Kind:         "control",
			Status:       "active",
			Title:        "Control " + c.citation,
			Ordinal:      ordinal,
		}

		// Parent: CLD.6.3.1 -> CLD.6.3.
		parentCite := isoCLDParent(c.citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if c.body != "" {
			body := c.body
			cr.Body = &body
		}

		citationToIdx[c.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

// parseISO27017Sections extracts numbered sections (5-18) and CLD entries.
func parseISO27017Sections(pages []isoPage) (sections []isoControlItem, cldItems []isoControlItem) {
	type lineInfo struct {
		page int
		text string
	}

	var allLines []lineInfo
	for _, p := range pages {
		for _, line := range strings.Split(p.Text, "\n") {
			allLines = append(allLines, lineInfo{page: p.N, text: strings.TrimSpace(line)})
		}
	}

	// Find all section and CLD headings.
	type heading struct {
		lineIdx  int
		citation string
		isCLD    bool
		page     int
	}

	var headings []heading
	seen := make(map[string]bool)

	for i, li := range allLines {
		if li.text == "" {
			continue
		}
		if isISOControlCatalogSkipLine(li.text) {
			continue
		}

		// CLD pattern.
		if m := reISOCLD.FindStringSubmatch(li.text); m != nil {
			id := m[1]
			if !seen[id] {
				seen[id] = true
				headings = append(headings, heading{lineIdx: i, citation: id, isCLD: true, page: li.page})
			}
			continue
		}

		// Numbered section.
		if m := reISOSubClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			parts := strings.Split(num, ".")
			mainN, err := strconv.Atoi(parts[0])
			if err == nil && mainN >= 5 && mainN <= 18 && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, isCLD: false, page: li.page})
			}
			continue
		}

		// Main clause heading (single digit).
		if m := reISOClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			n, err := strconv.Atoi(num)
			if err == nil && n >= 5 && n <= 18 && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, isCLD: false, page: li.page})
			}
		}
	}

	// Collect body for each heading.
	for idx, h := range headings {
		endLine := len(allLines)
		if idx+1 < len(headings) {
			endLine = headings[idx+1].lineIdx
		}

		var bodyParts []string
		for j := h.lineIdx + 1; j < endLine; j++ {
			text := allLines[j].text
			if text == "" {
				continue
			}
			if isISOControlCatalogSkipLine(text) {
				continue
			}
			bodyParts = append(bodyParts, text)
		}

		body := strings.Join(bodyParts, "\n")
		body = strings.TrimSpace(body)

		item := isoControlItem{citation: h.citation, page: h.page, body: body}
		if h.isCLD {
			cldItems = append(cldItems, item)
		} else {
			sections = append(sections, item)
		}
	}

	return
}

// buildISO27018Tree parses 27018:2019 — 27002:2013-keyed sections + Annex A PII controls.
// Inclusion rule: ALL numbered sections get rows (they are citable); sections
// that merely reference 27002 get a row with that reference body; sections with
// PII-specific additions get their full guidance body.
func buildISO27018Tree(cap isoCapture, versionLabel string) (*TreeResult, error) {
	result := &TreeResult{
		Title: "ISO/IEC 27018 " + versionLabel,
	}

	sections, annexItems := parseISO27018Sections(cap.Pages)

	if len(sections) == 0 && len(annexItems) == 0 {
		return nil, fmt.Errorf("no sections or Annex A controls found in ISO 27018 capture")
	}

	citationToIdx := make(map[string]int)
	var ordinal int32

	// Insert numbered sections.
	for _, s := range sections {
		cr := ControlRow{
			Citation:     s.citation,
			CitationNorm: strings.ToUpper(s.citation),
			Kind:         "control",
			Status:       "active",
			Title:        "Control " + s.citation,
			Ordinal:      ordinal,
		}

		parentCite := isoParentCitation(s.citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			cr.ParentIdx = -1
		}

		if s.body != "" {
			body := s.body
			cr.Body = &body
		}

		citationToIdx[s.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	// Insert Annex A PII controls.
	for _, a := range annexItems {
		cr := ControlRow{
			Citation:     a.citation,
			CitationNorm: strings.ToUpper(a.citation),
			Kind:         "annex-control",
			Status:       "active",
			Title:        "Annex A control " + a.citation,
			Ordinal:      ordinal,
		}

		// Parent: A.11.5 -> A.11, A.11 -> (root).
		parentCite := isoParentCitation(a.citation)
		if parentCite == "" {
			cr.ParentIdx = -1
		} else if idx, ok := citationToIdx[parentCite]; ok {
			cr.ParentIdx = idx
		} else {
			// Create implicit parent if not seen.
			cr.ParentIdx = -1
		}

		if a.body != "" {
			body := a.body
			cr.Body = &body
		}

		citationToIdx[a.citation] = len(result.Controls)
		result.Controls = append(result.Controls, cr)
		ordinal++
	}

	return result, nil
}

// parseISO27018Sections extracts numbered sections (5-18) and Annex A PII controls.
func parseISO27018Sections(pages []isoPage) (sections []isoControlItem, annexItems []isoControlItem) {
	type lineInfo struct {
		page int
		text string
	}

	var allLines []lineInfo
	for _, p := range pages {
		for _, line := range strings.Split(p.Text, "\n") {
			allLines = append(allLines, lineInfo{page: p.N, text: strings.TrimSpace(line)})
		}
	}

	type heading struct {
		lineIdx  int
		citation string
		isAnnex  bool
		page     int
	}

	var headings []heading
	seen := make(map[string]bool)

	for i, li := range allLines {
		if li.text == "" {
			continue
		}
		if isISOControlCatalogSkipLine(li.text) {
			continue
		}

		// Annex A (A.N.M) pattern.
		if m := reISO27018Annex.FindStringSubmatch(li.text); m != nil {
			id := m[1]
			if !seen[id] {
				seen[id] = true
				headings = append(headings, heading{lineIdx: i, citation: id, isAnnex: true, page: li.page})
			}
			continue
		}

		// Numbered section.
		if m := reISOSubClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			parts := strings.Split(num, ".")
			mainN, err := strconv.Atoi(parts[0])
			if err == nil && mainN >= 5 && mainN <= 18 && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, isAnnex: false, page: li.page})
			}
			continue
		}

		// Main clause heading.
		if m := reISOClause.FindStringSubmatch(li.text); m != nil {
			num := m[1]
			n, err := strconv.Atoi(num)
			if err == nil && n >= 5 && n <= 18 && !seen[num] {
				seen[num] = true
				headings = append(headings, heading{lineIdx: i, citation: num, isAnnex: false, page: li.page})
			}
		}
	}

	// Collect body.
	for idx, h := range headings {
		endLine := len(allLines)
		if idx+1 < len(headings) {
			endLine = headings[idx+1].lineIdx
		}

		var bodyParts []string
		for j := h.lineIdx + 1; j < endLine; j++ {
			text := allLines[j].text
			if text == "" {
				continue
			}
			if isISOControlCatalogSkipLine(text) {
				continue
			}
			bodyParts = append(bodyParts, text)
		}

		body := strings.Join(bodyParts, "\n")
		body = strings.TrimSpace(body)

		item := isoControlItem{citation: h.citation, page: h.page, body: body}
		if h.isAnnex {
			annexItems = append(annexItems, item)
		} else {
			sections = append(sections, item)
		}
	}

	return
}

// --- ISO 27002 attribute-table boilerplate strip ---

// stripISO27002AttributeTable removes the per-control attribute-table block that
// go-fitz reading-order extraction leaks into every 27002:2022 control body. The
// block runs from the start of the body to the first line that is exactly "Control"
// (the label preceding the normative control statement). The block contains:
//
//	Control type Information security properties
//	Cybersecurity
//	concepts
//	Operational
//	capabilities
//	Security domains
//	#tag1 #tag2 ...
//	Control              <-- strip boundary (this line removed too)
//
// Returns the body with the boilerplate stripped. If no "Control" boundary line
// is found, the body is returned unchanged (defensive — don't corrupt).
func stripISO27002AttributeTable(body string) string {
	lines := strings.Split(body, "\n")

	// Find the "Control" boundary line within the first ~20 lines.
	// The attribute table always contains "#tag" lines (attribute hashtags)
	// before the "Control" label. Require at least one preceding line
	// starting with "#" to distinguish the real boundary from a body line
	// that happens to be exactly "Control".
	boundary := -1
	for i, line := range lines {
		if i > 20 {
			break // safety: if not found in the first 20 lines, bail
		}
		if strings.TrimSpace(line) != "Control" {
			continue
		}
		// Require at least one preceding "#tag" line (attribute hashtag).
		hasTagLine := false
		for k := 0; k < i; k++ {
			if strings.HasPrefix(strings.TrimSpace(lines[k]), "#") {
				hasTagLine = true
				break
			}
		}
		if hasTagLine {
			boundary = i
			break
		}
	}

	if boundary < 0 {
		// No boundary found — return unchanged.
		return body
	}

	// Strip everything up to and including the boundary line.
	remaining := lines[boundary+1:]
	return strings.TrimSpace(strings.Join(remaining, "\n"))
}

// --- Shared helpers ---

// isISOControlCatalogSkipLine filters structural artifacts from ISO control docs.
func isISOControlCatalogSkipLine(line string) bool {
	if strings.HasPrefix(line, "ISO/IEC") {
		return true
	}
	if strings.Contains(line, "All rights reserved") {
		return true
	}
	// Bare page numbers.
	if reISOBarePageNum.MatchString(line) {
		return true
	}
	// Table-of-contents lines: heading followed by dots and page number.
	if strings.Contains(line, "...") {
		return true
	}
	return false
}

// isoParentCitation returns the parent citation for a dotted ISO citation.
// E.g. "A.5.1" -> "A.5", "6.1.3" -> "6.1", "4" -> "".
func isoParentCitation(citation string) string {
	lastDot := strings.LastIndex(citation, ".")
	if lastDot < 0 {
		return ""
	}
	return citation[:lastDot]
}

// isoCLDParent returns the parent citation for a CLD.* citation.
// CLD.6.3.1 -> CLD.6.3, CLD.6.3 -> "" (top-level CLD section).
func isoCLDParent(citation string) string {
	// CLD.x.y.z -> CLD.x.y; CLD.x.y -> ""
	parts := strings.Split(citation, ".")
	if len(parts) <= 3 {
		// CLD.x.y is a top-level CLD section.
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

// isoCompare compares two dotted ISO citations for sorting.
// Handles A.N.M and plain N.M formats.
func isoCompare(a, b string) int {
	pa := isoCitationParts(a)
	pb := isoCitationParts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			// Try numeric comparison.
			na, ea := strconv.Atoi(pa[i])
			nb, eb := strconv.Atoi(pb[i])
			if ea == nil && eb == nil {
				return na - nb
			}
			// String comparison for non-numeric parts (A, CLD, etc.).
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return len(pa) - len(pb)
}

// isoCitationParts splits a citation like "A.5.1" into ["A", "5", "1"].
func isoCitationParts(citation string) []string {
	return strings.Split(citation, ".")
}
