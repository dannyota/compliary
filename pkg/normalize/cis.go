package normalize

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// BuildCISTree parses a workbook-rows-json capture for CIS Controls v8.1 and
// returns the normalized control tree. This is a pure function with no side
// effects.
//
// Tree shape: 18 control rows (kind 'control') + 153 safeguard rows
// (kind 'safeguard'). Controls carry citation = the number string (e.g. "4"),
// title from col E, body from col F. Safeguards carry citation = the captured
// safeguard ID string from col B (e.g. "4.10" — used verbatim, never float-
// parsed), parent = their control, title from col E, body = col F description
// + labeled attribute lines (Asset Class, Security Function, Implementation
// Groups) appended after a blank line. No mapping edges.
func BuildCISTree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var wb csfWorkbook // reuse the workbook capture types from csf.go
	if err := json.Unmarshal(raw, &wb); err != nil {
		return nil, fmt.Errorf("unmarshal workbook: %w", err)
	}

	// Find the "Controls v8.1.2" data sheet (match any sheet containing "Controls v8").
	var dataSheet *csfSheet
	for i := range wb.Sheets {
		if strings.Contains(wb.Sheets[i].Name, "Controls v8") {
			dataSheet = &wb.Sheets[i]
			break
		}
	}
	if dataSheet == nil {
		return nil, fmt.Errorf("no Controls data sheet found (expected name containing 'Controls v8')")
	}

	// Parse cells into row-indexed grid.
	grid := parseGrid(dataSheet.Rows)

	// Collect all row numbers, sorted. Skip header row 1.
	var rowNums []int
	for r := range grid {
		if r > 1 {
			rowNums = append(rowNums, r)
		}
	}
	slices.Sort(rowNums)

	result := &TreeResult{}
	if strings.HasPrefix(versionLabel, "v") {
		result.Title = "CIS Controls " + versionLabel
	} else {
		result.Title = "CIS Controls v" + versionLabel
	}

	// Track current control index for parent linkage.
	controlIdxByNum := make(map[string]int) // control number → index in result.Controls
	var ordinal int32

	for _, r := range rowNums {
		cols := grid[r]
		colA := strings.TrimSpace(cols["A"])
		colB := strings.TrimSpace(cols["B"])
		colC := strings.TrimSpace(cols["C"]) // Asset Class
		colD := strings.TrimSpace(cols["D"]) // Security Function
		colE := strings.TrimSpace(cols["E"]) // Title
		colF := strings.TrimSpace(cols["F"]) // Description
		colG := strings.TrimSpace(cols["G"]) // IG1
		colH := strings.TrimSpace(cols["H"]) // IG2
		colI := strings.TrimSpace(cols["I"]) // IG3

		// Trim non-breaking spaces (U+00A0) from col A — control header rows
		// in the CIS workbook sometimes carry a trailing NBSP.
		colA = strings.TrimRight(colA, "  ")

		if colA == "" {
			continue // skip empty rows
		}

		if colB == "" {
			// Control header row: col A is the control number, col B is empty.
			cr := ControlRow{
				Citation:     colA,
				CitationNorm: colA, // digits only, already uppercase-free
				Kind:         "control",
				Status:       "active",
				Title:        colE,
				ParentIdx:    -1,
				Ordinal:      ordinal,
			}
			if colE != "" {
				cr.TitleOriginal = strPtr(colE)
			}
			if colF != "" {
				cr.Body = strPtr(colF)
			}

			controlIdxByNum[colA] = len(result.Controls)
			result.Controls = append(result.Controls, cr)
			ordinal++
		} else {
			// Safeguard row: col B is the safeguard ID, col A is the parent
			// control number.
			parentIdx := -1
			if idx, ok := controlIdxByNum[colA]; ok {
				parentIdx = idx
			}

			// Build body: description + attribute lines.
			body := buildCISSafeguardBody(colF, colC, colD, colG, colH, colI)

			cr := ControlRow{
				Citation:     colB, // verbatim captured string (e.g. "4.10")
				CitationNorm: colB, // digits and dot, already uppercase-free
				Kind:         "safeguard",
				Status:       "active",
				Title:        colE,
				ParentIdx:    parentIdx,
				Ordinal:      ordinal,
			}
			if colE != "" {
				cr.TitleOriginal = strPtr(colE)
			}
			cr.Body = body

			result.Controls = append(result.Controls, cr)
			ordinal++
		}
	}

	return result, nil
}

// buildCISSafeguardBody constructs the safeguard body from description and
// attribute columns. Format:
//
//	<description>
//
//	Asset Class: <value>
//	Security Function: <value>
//	Implementation Groups: IG1, IG2, IG3
//
// Attribute lines are only included when the value is non-empty. The blank
// line separator is only emitted when there are attribute lines.
func buildCISSafeguardBody(description, assetClass, securityFunction, ig1, ig2, ig3 string) *string {
	var attrs []string

	if assetClass != "" {
		attrs = append(attrs, "Asset Class: "+assetClass)
	}
	if securityFunction != "" {
		attrs = append(attrs, "Security Function: "+securityFunction)
	}

	// Build IG line: only listed IGs are included.
	var igs []string
	if strings.EqualFold(ig1, "x") {
		igs = append(igs, "IG1")
	}
	if strings.EqualFold(ig2, "x") {
		igs = append(igs, "IG2")
	}
	if strings.EqualFold(ig3, "x") {
		igs = append(igs, "IG3")
	}
	if len(igs) > 0 {
		attrs = append(attrs, "Implementation Groups: "+strings.Join(igs, ", "))
	}

	if description == "" && len(attrs) == 0 {
		return nil
	}

	var body string
	if description != "" && len(attrs) > 0 {
		body = description + "\n\n" + strings.Join(attrs, "\n")
	} else if description != "" {
		body = description
	} else {
		body = strings.Join(attrs, "\n")
	}

	return &body
}
