package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// csfSheet is the parsed representation of one sheet from a workbook-rows-json
// capture: {"sheets":[{"name":"...","rows":[{"ref":"A5","value":"..."}]}]}.
type csfSheet struct {
	Name string    `json:"name"`
	Rows []csfCell `json:"rows"`
}

type csfCell struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

type csfWorkbook struct {
	Sheets []csfSheet `json:"sheets"`
}

// csfParsedRow is the intermediate parse result for one data row.
type csfParsedRow struct {
	SheetRow int
	Kind     string // function, category, subcategory
	ID       string // e.g. "GV", "GV.OC", "GV.OC-01"
	Title    string // the statement text
	Status   string // active, withdrawn
	Body     *string
	// Withdrawal note fields (for incorporated-into / moved-to edges).
	WithdrawalAction  string   // "incorporated-into" or "moved-to"; empty for active
	WithdrawalTargets []string // target IDs parsed from the bracket note
	// Cell reference for provenance_detail.
	CellRef string // e.g. "C49"
	// Informative References (col E) — raw text, split into lines by the ref parser.
	ColE string
}

// csfFunctionID extracts the function code from a function cell, e.g.
// "GOVERN (GV): description..." → "GV", "description..."
// "GOVERN (GV)" → "GV", ""
var reFuncID = regexp.MustCompile(`^[A-Z]+\s+\(([A-Z]{2})\)(?::\s*(.+))?$`)

// reCatID extracts category ID from a category cell, e.g.
// "Organizational Context (GV.OC): statement..." → "GV.OC", "Organizational Context", "statement..."
// "Business Environment (ID.BE): [Withdrawn: ...]" → "ID.BE", "Business Environment", "[Withdrawn: ...]"
var reCatID = regexp.MustCompile(`^(.+?)\s+\(([A-Z]{2}\.[A-Z]{2,})\):\s*(.+)$`)

// reSubID extracts subcategory ID from a subcategory cell, e.g.
// "GV.OC-01: statement text" → "GV.OC-01", "statement text"
// "ID.AM-06: [Withdrawn: ...]" → "ID.AM-06", "[Withdrawn: ...]"
var reSubID = regexp.MustCompile(`^([A-Z]{2}\.[A-Z]{2,}-\d+):\s*(.+)$`)

// reWithdrawn parses the withdrawal bracket note.
// "[Withdrawn: Incorporated into X, Y]" or "[Withdrawn: Moved to X]" or "[Withdrawn: Moved into X]"
var reWithdrawn = regexp.MustCompile(`^\[Withdrawn:\s*(.+)\]$`)

// reTargetID matches a valid CSF ID (function, category, or subcategory).
var reTargetID = regexp.MustCompile(`^[A-Z]{2}(?:\.[A-Z]{2,}(?:-\d+)?)?$`)

// BuildCSFTree parses a workbook-rows-json capture for NIST CSF 2.0 and returns
// the normalized control tree with informative-reference mapping edges. This is
// a pure function with no side effects.
//
// refSources maps informative-reference prefixes to target frameworks. When
// nil or empty, no reference edges are emitted (backward compatible).
func BuildCSFTree(raw json.RawMessage, frameworkCode, versionLabel string, refSources ...ReferenceSource) (*TreeResult, error) {
	var wb csfWorkbook
	if err := json.Unmarshal(raw, &wb); err != nil {
		return nil, fmt.Errorf("unmarshal workbook: %w", err)
	}

	// Find the "CSF 2.0" data sheet.
	var dataSheet *csfSheet
	for i := range wb.Sheets {
		if wb.Sheets[i].Name == "CSF 2.0" {
			dataSheet = &wb.Sheets[i]
			break
		}
	}
	if dataSheet == nil {
		return nil, fmt.Errorf("sheet 'CSF 2.0' not found")
	}

	// Parse cells into row-indexed grid.
	grid := parseGrid(dataSheet.Rows)

	// Parse data rows (skip header row 2 and anything before it).
	parsed, err := parseCSFRows(grid)
	if err != nil {
		return nil, err
	}

	// Build reference source lookup.
	refMap := make(map[string]ReferenceSource, len(refSources))
	for _, rs := range refSources {
		refMap[rs.Prefix] = rs
	}

	// Build control tree from parsed rows.
	return buildCSFControlTree(parsed, frameworkCode, versionLabel, refMap)
}

// parseGrid converts a flat cell list into a map[row]map[col]value.
func parseGrid(cells []csfCell) map[int]map[string]string {
	grid := make(map[int]map[string]string)
	for _, c := range cells {
		col, row := splitRef(c.Ref)
		if row == 0 {
			continue
		}
		if grid[row] == nil {
			grid[row] = make(map[string]string)
		}
		grid[row][col] = c.Value
	}
	return grid
}

// splitRef splits a cell reference like "A5" into column "A" and row 5.
func splitRef(ref string) (string, int) {
	var col strings.Builder
	row := 0
	for i := 0; i < len(ref); i++ {
		ch := ref[i]
		if ch >= 'A' && ch <= 'Z' {
			col.WriteByte(ch)
		} else if ch >= '0' && ch <= '9' {
			row = row*10 + int(ch-'0')
		}
	}
	return col.String(), row
}

// parseCSFRows processes the grid into typed parsed rows, deduplicating
// function rows (each function appears twice: once with description, once bare;
// keep the described row).
func parseCSFRows(grid map[int]map[string]string) ([]csfParsedRow, error) {
	// Collect all row numbers, sorted.
	var rowNums []int
	for r := range grid {
		if r > 2 { // skip header row 2 and anything above
			rowNums = append(rowNums, r)
		}
	}
	slices.Sort(rowNums)

	// First pass: identify function rows and dedupe by ID (keep the one with description).
	type funcInfo struct {
		row     int
		id      string
		title   string
		colE    string
		hasDesc bool
	}
	funcSeen := make(map[string]*funcInfo)

	for _, r := range rowNums {
		a := strings.TrimSpace(grid[r]["A"])
		if a == "" {
			continue
		}
		m := reFuncID.FindStringSubmatch(a)
		if m == nil {
			return nil, fmt.Errorf("row %d: cannot parse function from col A: %q", r, a)
		}
		funcID := m[1]
		desc := strings.TrimSpace(m[2])
		hasDesc := desc != ""
		colE := strings.TrimSpace(grid[r]["E"])

		if existing, ok := funcSeen[funcID]; ok {
			// Keep the described one.
			if hasDesc && !existing.hasDesc {
				existing.row = r
				existing.title = desc
				existing.colE = colE
				existing.hasDesc = true
			}
		} else {
			funcSeen[funcID] = &funcInfo{
				row: r, id: funcID, title: desc, colE: colE, hasDesc: hasDesc,
			}
		}
	}

	// Second pass: build all parsed rows in sheet order.
	var result []csfParsedRow
	emittedFuncs := make(map[string]bool)

	for _, r := range rowNums {
		cols := grid[r]
		a := strings.TrimSpace(cols["A"])
		b := strings.TrimSpace(cols["B"])
		c := strings.TrimSpace(cols["C"])
		d := strings.TrimSpace(cols["D"])
		e := strings.TrimSpace(cols["E"])

		if a != "" {
			// Function row — emit only the described one (first encounter after dedupe).
			m := reFuncID.FindStringSubmatch(a)
			if m == nil {
				continue // shouldn't happen, caught in first pass
			}
			funcID := m[1]
			if emittedFuncs[funcID] {
				continue // skip the duplicate
			}
			emittedFuncs[funcID] = true
			fi := funcSeen[funcID]

			result = append(result, csfParsedRow{
				SheetRow: fi.row,
				Kind:     "function",
				ID:       funcID,
				Title:    fi.title,
				Status:   "active", // functions are never withdrawn
				CellRef:  fmt.Sprintf("A%d", fi.row),
				ColE:     fi.colE,
			})
			continue
		}

		if b != "" {
			// Category row.
			pr, err := parseCategoryRow(r, b)
			if err != nil {
				return nil, err
			}
			pr.ColE = e
			result = append(result, pr)
			continue
		}

		if c != "" {
			// Subcategory row.
			pr, err := parseSubcategoryRow(r, c, d)
			if err != nil {
				return nil, err
			}
			pr.ColE = e
			result = append(result, pr)
			continue
		}
	}

	return result, nil
}

// parseCategoryRow parses a category cell like
// "Organizational Context (GV.OC): statement text" or
// "Business Environment (ID.BE): [Withdrawn: Incorporated into GV.OC]".
func parseCategoryRow(sheetRow int, b string) (csfParsedRow, error) {
	m := reCatID.FindStringSubmatch(b)
	if m == nil {
		return csfParsedRow{}, fmt.Errorf("row %d: cannot parse category from col B: %q", sheetRow, b)
	}
	// m[1] = title name, m[2] = ID, m[3] = rest (statement or withdrawal note)
	catID := m[2]
	rest := strings.TrimSpace(m[3])

	pr := csfParsedRow{
		SheetRow: sheetRow,
		Kind:     "category",
		ID:       catID,
		CellRef:  fmt.Sprintf("B%d", sheetRow),
	}

	if wm := reWithdrawn.FindStringSubmatch(rest); wm != nil {
		pr.Status = "withdrawn"
		pr.Title = m[1] // the name part before the ID
		parseWithdrawalNote(wm[1], &pr)
	} else {
		pr.Status = "active"
		pr.Title = rest // the statement text
	}

	return pr, nil
}

// parseSubcategoryRow parses a subcategory cell like
// "GV.OC-01: statement text" or
// "ID.AM-06: [Withdrawn: Incorporated into GV.RR-02, GV.SC-02]".
func parseSubcategoryRow(sheetRow int, c, d string) (csfParsedRow, error) {
	m := reSubID.FindStringSubmatch(c)
	if m == nil {
		return csfParsedRow{}, fmt.Errorf("row %d: cannot parse subcategory from col C: %q", sheetRow, c)
	}
	subID := m[1]
	rest := strings.TrimSpace(m[2])

	pr := csfParsedRow{
		SheetRow: sheetRow,
		Kind:     "subcategory",
		ID:       subID,
		CellRef:  fmt.Sprintf("C%d", sheetRow),
	}

	if wm := reWithdrawn.FindStringSubmatch(rest); wm != nil {
		pr.Status = "withdrawn"
		parseWithdrawalNote(wm[1], &pr)
	} else {
		pr.Status = "active"
		pr.Title = rest

		// Build body: statement text, then examples if present.
		body := rest
		if d != "" {
			body += "\n\n" + d
		}
		pr.Body = &body
	}

	return pr, nil
}

// parseWithdrawalNote parses the inner text of a withdrawal bracket note like
// "Incorporated into GV.OC-05" or "Moved to PR.AA" or "Moved into GV.RM-02".
// It sets WithdrawalAction and WithdrawalTargets on the parsed row.
// Targets matching "other ..." are free-text and produce no edges.
func parseWithdrawalNote(note string, pr *csfParsedRow) {
	note = strings.TrimSpace(note)

	if strings.HasPrefix(note, "Incorporated into") {
		pr.WithdrawalAction = "incorporated-into"
		targetsStr := strings.TrimSpace(note[len("Incorporated into"):])
		parseTargetList(targetsStr, pr)
	} else if strings.HasPrefix(note, "Moved to") {
		pr.WithdrawalAction = "moved-to"
		targetsStr := strings.TrimSpace(note[len("Moved to"):])
		parseTargetList(targetsStr, pr)
	} else if strings.HasPrefix(note, "Moved into") {
		// One row uses "Moved into" instead of "Moved to" — treat identically.
		pr.WithdrawalAction = "moved-to"
		targetsStr := strings.TrimSpace(note[len("Moved into"):])
		parseTargetList(targetsStr, pr)
	}
}

// parseTargetList splits a comma-separated target list and filters to valid IDs.
func parseTargetList(s string, pr *csfParsedRow) {
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if reTargetID.MatchString(p) {
			pr.WithdrawalTargets = append(pr.WithdrawalTargets, p)
		}
		// else: free-text like "other Categories and Functions" — skip, no edge
	}
}

// buildCSFControlTree converts parsed rows into the TreeResult.
func buildCSFControlTree(parsed []csfParsedRow, frameworkCode, versionLabel string, refMap map[string]ReferenceSource) (*TreeResult, error) {
	result := &TreeResult{
		Title: "NIST Cybersecurity Framework (CSF) 2.0",
	}

	// Track parent indices by ID for parentage assignment.
	idToIdx := make(map[string]int)
	// Track current function index for category parentage.
	var currentFuncIdx int = -1

	var ordinal int32
	for _, pr := range parsed {
		cr := ControlRow{
			Citation:     pr.ID,
			CitationNorm: strings.ToUpper(pr.ID),
			Kind:         pr.Kind,
			Status:       pr.Status,
			Title:        pr.Title,
			Ordinal:      ordinal,
		}

		// title_original = title for CSF (public domain, same text).
		if pr.Title != "" {
			cr.TitleOriginal = strPtr(pr.Title)
		}

		// Body.
		cr.Body = pr.Body

		// Parent linkage.
		switch pr.Kind {
		case "function":
			cr.ParentIdx = -1
			currentFuncIdx = len(result.Controls)
		case "category":
			// Active categories parent to current function.
			// Withdrawn categories also parent to current function.
			if currentFuncIdx >= 0 {
				cr.ParentIdx = currentFuncIdx
			} else {
				cr.ParentIdx = -1
			}
		case "subcategory":
			// Active subcategories parent to the preceding active category.
			// Withdrawn subcategories parent to their withdrawn v1.1 category
			// when it exists in-sheet, else to the function row.
			parentIdx := findSubcategoryParent(pr, idToIdx, currentFuncIdx)
			cr.ParentIdx = parentIdx
		}

		idx := len(result.Controls)
		idToIdx[pr.ID] = idx
		result.Controls = append(result.Controls, cr)
		ordinal++

		// Withdrawal mapping edges.
		if pr.Status == "withdrawn" && pr.WithdrawalAction != "" {
			for _, target := range pr.WithdrawalTargets {
				result.Mappings = append(result.Mappings, MappingEdge{
					FromIdx:          idx,
					ToFrameworkCode:  frameworkCode,
					ToVersionLabel:   strPtr(versionLabel),
					ToCitationNorm:   strings.ToUpper(target),
					MappingSource:    "publisher-catalog",
					Relationship:     pr.WithdrawalAction,
					ProvenanceDetail: "CSF 2.0!" + pr.CellRef,
				})
			}
		}
	}

	// Informative-reference mapping edges from col E.
	if len(refMap) > 0 {
		skips := &RefSkips{
			PerPrefix:  make(map[string]int),
			UnknownPfx: make(map[string]int),
		}
		result.RefSkips = skips

		for i, pr := range parsed {
			if pr.ColE == "" {
				continue
			}
			idx := idToIdx[pr.ID]
			sheetRow := pr.SheetRow
			parseCSFReferences(pr.ColE, idx, sheetRow, refMap, result, skips)
			_ = i
		}
	}

	return result, nil
}

// parseCSFReferences processes the col E text for one CSF row, splitting into
// lines, looking up each prefix in the reference source map, extracting the
// citation, and emitting MappingEdge entries. Handles dedupe across prefixes
// that map to the same target (e.g. SP 800-53 Rev 5.1.1 and 5.2.0 both map
// to nist80053/r5): uses a local seen map keyed on the natural key, with
// provenance_detail joining the release strings of all contributing prefixes.
func parseCSFReferences(
	colE string,
	controlIdx int,
	sheetRow int,
	refMap map[string]ReferenceSource,
	result *TreeResult,
	skips *RefSkips,
) {
	lines := strings.Split(colE, "\n")
	cellRef := fmt.Sprintf("E%d", sheetRow)

	// Dedupe key: (to_framework_code, to_version_label_str, citation_norm)
	// Value: index into result.Mappings + list of release strings for provenance.
	type edgeKey struct {
		toFW  string
		toVer string // "" for NULL
		cite  string
	}
	type edgeInfo struct {
		mappingIdx int
		releases   []string
	}
	seen := make(map[edgeKey]*edgeInfo)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Colon-split: find the first ':'
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		prefix := strings.TrimSpace(line[:colonIdx])
		rest := strings.TrimSpace(line[colonIdx+1:])

		rs, ok := refMap[prefix]
		if !ok {
			skips.UnknownPfx[prefix]++
			continue
		}

		// Parse citation from rest based on prefix rules.
		cite, skip := parseRefCitation(prefix, rest)
		if skip {
			skips.PerPrefix[prefix]++
			continue
		}

		citationNorm := strings.ToUpper(strings.TrimSpace(cite))

		// Build version label string for dedup key.
		verStr := ""
		if rs.ToVersionLabel != nil {
			verStr = *rs.ToVersionLabel
		}

		dk := edgeKey{toFW: rs.ToFrameworkCode, toVer: verStr, cite: citationNorm}
		if existing, ok := seen[dk]; ok {
			// Dedupe: add this prefix's release string to provenance.
			existing.releases = append(existing.releases, prefix)
			// Update the provenance_detail on the existing mapping.
			slices.Sort(existing.releases)
			result.Mappings[existing.mappingIdx].ProvenanceDetail = cellRef + " [" + strings.Join(existing.releases, "; ") + "]"
			continue
		}

		mappingIdx := len(result.Mappings)
		provenance := cellRef + " [" + prefix + "]"

		result.Mappings = append(result.Mappings, MappingEdge{
			FromIdx:          controlIdx,
			ToFrameworkCode:  rs.ToFrameworkCode,
			ToVersionLabel:   rs.ToVersionLabel,
			ToCitationNorm:   citationNorm,
			MappingSource:    rs.MappingSourceCode,
			Relationship:     "related",
			ProvenanceDetail: provenance,
		})

		seen[dk] = &edgeInfo{
			mappingIdx: mappingIdx,
			releases:   []string{prefix},
		}
	}
}

// parseRefCitation extracts and normalizes a citation from the rest-of-line
// after stripping the prefix. Returns (citation, skip). skip=true means this
// line should be counted as a skip (unparseable/empty).
func parseRefCitation(prefix, rest string) (string, bool) {
	switch {
	case prefix == "ISO/IEC 27001":
		return parseISOCitation(rest)
	default:
		// All other prefixes: the rest is the citation directly.
		cite := strings.TrimSpace(rest)
		if cite == "" {
			return "", true
		}
		return cite, false
	}
}

// parseISOCitation handles the ISO/IEC 27001 citation format:
// "2022: Annex A Controls: 5.1" → "5.1"
// "2022: Mandatory Clause: 8.1" → "8.1"
// "2022: Control 5.8" → "5.8"
// "2022: Mandatory Clause: None" → skip
// "2022: Annex A Controls:" (bare) → skip
// Lines not starting with "2022:" are skipped (edition mismatch).
func parseISOCitation(rest string) (string, bool) {
	// Verify edition echo.
	if !strings.HasPrefix(rest, "2022:") {
		return "", true // edition mismatch
	}
	after := strings.TrimSpace(rest[5:])

	var cite string
	switch {
	case strings.HasPrefix(after, "Annex A Controls:"):
		cite = strings.TrimSpace(after[len("Annex A Controls:"):])
	case strings.HasPrefix(after, "Mandatory Clause:"):
		cite = strings.TrimSpace(after[len("Mandatory Clause:"):])
	case strings.HasPrefix(after, "Control"):
		cite = strings.TrimSpace(after[len("Control"):])
	default:
		return "", true // unknown sub-form
	}

	if cite == "" || strings.EqualFold(cite, "none") {
		return "", true
	}

	// Strip trailing comma (publisher typo).
	cite = strings.TrimRight(cite, ",")
	cite = strings.TrimSpace(cite)

	// Multi-cite lines (contain comma after cleanup) are skipped.
	if strings.Contains(cite, ",") {
		return "", true
	}

	return cite, false
}

// findSubcategoryParent determines the parent index for a subcategory row.
// The parent is determined by the subcategory's ID prefix (the category part):
//   - For active subcategories: parent is the matching active category.
//   - For withdrawn subcategories: parent is the matching withdrawn category
//     if it exists, otherwise the current function.
func findSubcategoryParent(pr csfParsedRow, idToIdx map[string]int, currentFuncIdx int) int {
	// Extract category prefix from subcategory ID: "GV.OC-01" → "GV.OC"
	catPrefix := extractCategoryPrefix(pr.ID)
	if catPrefix == "" {
		if currentFuncIdx >= 0 {
			return currentFuncIdx
		}
		return -1
	}

	if idx, ok := idToIdx[catPrefix]; ok {
		return idx
	}

	// Category not found (shouldn't happen with well-formed data).
	if currentFuncIdx >= 0 {
		return currentFuncIdx
	}
	return -1
}

// extractCategoryPrefix extracts the category part from a subcategory ID.
// "GV.OC-01" → "GV.OC", "ID.BE-05" → "ID.BE"
func extractCategoryPrefix(subID string) string {
	dashIdx := strings.LastIndex(subID, "-")
	if dashIdx < 0 {
		return ""
	}
	return subID[:dashIdx]
}
