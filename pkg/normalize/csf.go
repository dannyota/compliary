package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
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

// csfRowData holds the parsed cell values for one sheet row.
type csfRowData struct {
	Row int
	A   string // Function
	B   string // Category
	C   string // Subcategory
	D   string // Implementation Examples
	E   string // Informative References (Task 3)
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
// the normalized control tree. This is a pure function with no side effects.
//
// The row parser is structured so that Task 3 (informative-reference mapping
// edges from col E) can add edge emission without reshaping it: each csfRowData
// carries the E column, and the MappingEdge slice in TreeResult is ready.
func BuildCSFTree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
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

	// Build control tree from parsed rows.
	return buildCSFControlTree(parsed, frameworkCode, versionLabel)
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
	col := ""
	row := 0
	for _, ch := range ref {
		if ch >= 'A' && ch <= 'Z' {
			col += string(ch)
		} else if ch >= '0' && ch <= '9' {
			row = row*10 + int(ch-'0')
		}
	}
	return col, row
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
	sortInts(rowNums)

	// First pass: identify function rows and dedupe by ID (keep the one with description).
	type funcInfo struct {
		row     int
		id      string
		title   string
		hasDesc bool
	}
	funcSeen := make(map[string]*funcInfo)
	var funcOrder []string

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

		if existing, ok := funcSeen[funcID]; ok {
			// Keep the described one.
			if hasDesc && !existing.hasDesc {
				existing.row = r
				existing.title = desc
				existing.hasDesc = true
			}
		} else {
			funcOrder = append(funcOrder, funcID)
			funcSeen[funcID] = &funcInfo{
				row: r, id: funcID, title: desc, hasDesc: hasDesc,
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
		// E column preserved for Task 3.
		// e := strings.TrimSpace(cols["E"])

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
			})
			continue
		}

		if b != "" {
			// Category row.
			pr, err := parseCategoryRow(r, b)
			if err != nil {
				return nil, err
			}
			result = append(result, pr)
			continue
		}

		if c != "" {
			// Subcategory row.
			pr, err := parseSubcategoryRow(r, c, d)
			if err != nil {
				return nil, err
			}
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
func buildCSFControlTree(parsed []csfParsedRow, frameworkCode, versionLabel string) (*TreeResult, error) {
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
			parentIdx := findSubcategoryParent(pr, parsed, idToIdx, currentFuncIdx)
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

	return result, nil
}

// findSubcategoryParent determines the parent index for a subcategory row.
// The parent is determined by the subcategory's ID prefix (the category part):
//   - For active subcategories: parent is the matching active category.
//   - For withdrawn subcategories: parent is the matching withdrawn category
//     if it exists, otherwise the current function.
func findSubcategoryParent(pr csfParsedRow, parsed []csfParsedRow, idToIdx map[string]int, currentFuncIdx int) int {
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

// sortInts sorts a slice of ints in ascending order (simple insertion sort;
// the slice is small — ~230 rows).
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
