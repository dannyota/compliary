package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ccmColMap maps column letters to their semantic roles on the CCM sheet.
// Header row 3: A=Control Domain, B=Control Title, C=Control ID,
// D=Control Specification, E=CCM Lite, F=IaaS, G=PaaS, H=SaaS,
// I-N=architectural relevance, O-W=organizational relevance.
var ccmApplicabilityCols = []struct {
	Col   string
	Label string
}{
	{"E", "CCM Lite"},
	{"F", "IaaS"},
	{"G", "PaaS"},
	{"H", "SaaS"},
	{"I", "Phys"},
	{"J", "Network"},
	{"K", "Compute"},
	{"L", "Storage"},
	{"M", "App"},
	{"N", "Data"},
	{"O", "Cybersecurity"},
	{"P", "Internal Audit"},
	{"Q", "Architecture Team"},
	{"R", "SW Development"},
	{"S", "Operations"},
	{"T", "Legal/Privacy"},
	{"U", "GRC Team"},
	{"V", "Supply Chain Management"},
	{"W", "HR"},
}

// reDomainACR validates that the acronym portion of a domain header
// is an uppercase letter sequence (may include &), e.g. "A&A", "DCS", "I&S".
var reDomainACR = regexp.MustCompile(`^[A-Z][A-Z&]+$`)

// parseDomainHeader parses a CCM domain header string of the form
// "Domain Name - ACR" and returns (name, acronym, ok).
// Returns ok=false for trailer rows ("End of Standard", copyright notices, etc.).
func parseDomainHeader(s string) (string, string, bool) {
	// Find the last occurrence of " - " as the separator.
	idx := strings.LastIndex(s, " - ")
	if idx < 0 {
		return "", "", false
	}
	name := strings.TrimSpace(s[:idx])
	acr := strings.TrimSpace(s[idx+3:])

	// Validate acronym: must be uppercase letters (with optional &).
	if !reDomainACR.MatchString(acr) {
		return "", "", false
	}

	return name, acr, true
}

// BuildCCMTree parses a workbook-rows-json capture for CSA CCM v4.1 and returns
// the normalized control tree. This is a pure function with no side effects.
//
// Tree shape: domain rows (kind 'domain') + control rows (kind 'control').
// No mapping edges (the Mappings sheet contains only "This dataset is not
// available yet" in v4.1.0).
func BuildCCMTree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var wb struct {
		Sheets []struct {
			Name string `json:"name"`
			Rows []struct {
				Ref   string `json:"ref"`
				Value string `json:"value"`
			} `json:"rows"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(raw, &wb); err != nil {
		return nil, fmt.Errorf("unmarshal workbook: %w", err)
	}

	// Find the "CCM" data sheet.
	var ccmSheetIdx int = -1
	for i := range wb.Sheets {
		if wb.Sheets[i].Name == "CCM" {
			ccmSheetIdx = i
			break
		}
	}
	if ccmSheetIdx < 0 {
		return nil, fmt.Errorf("sheet 'CCM' not found")
	}

	// Parse cells into row-indexed grid.
	grid := make(map[int]map[string]string)
	for _, c := range wb.Sheets[ccmSheetIdx].Rows {
		col, row := splitRef(c.Ref)
		if row == 0 {
			continue
		}
		if grid[row] == nil {
			grid[row] = make(map[string]string)
		}
		grid[row][col] = c.Value
	}

	// Find max row number.
	maxRow := 0
	for r := range grid {
		if r > maxRow {
			maxRow = r
		}
	}

	result := &TreeResult{
		Title: fmt.Sprintf("CSA Cloud Controls Matrix (CCM) %s", versionLabel),
	}

	// Track current domain index for parent linkage.
	var currentDomainIdx int = -1
	// Track domain citation → index for parentage verification.
	domainIdx := make(map[string]int)

	var ordinal int32

	// Process rows 4+ (skip preamble rows 1-2 and header row 3).
	for r := 4; r <= maxRow; r++ {
		cols := grid[r]
		if cols == nil {
			continue
		}

		colA := strings.TrimSpace(cols["A"])
		colB := strings.TrimSpace(cols["B"])
		colC := strings.TrimSpace(cols["C"])
		colD := strings.TrimSpace(cols["D"])

		// Domain header: col A set, col C empty, parseable as "Name - ACR".
		if colA != "" && colC == "" {
			name, acr, ok := parseDomainHeader(colA)
			if !ok {
				// Trailer row (End of Standard, copyright, etc.) — skip.
				continue
			}

			cr := ControlRow{
				Citation:     acr,
				CitationNorm: strings.ToUpper(acr),
				Kind:         "domain",
				Status:       "active",
				Title:        name,
				ParentIdx:    -1,
				Ordinal:      ordinal,
			}
			// title_original = name for domains.
			cr.TitleOriginal = strPtr(name)

			currentDomainIdx = len(result.Controls)
			domainIdx[acr] = currentDomainIdx
			result.Controls = append(result.Controls, cr)
			ordinal++
			continue
		}

		// Control row: col C set (control ID).
		if colC != "" {
			cr := ControlRow{
				Citation:     colC,
				CitationNorm: strings.ToUpper(colC),
				Kind:         "control",
				Status:       "active",
				Title:        colB,
				ParentIdx:    currentDomainIdx,
				Ordinal:      ordinal,
			}

			// title_original = col B heading.
			if colB != "" {
				cr.TitleOriginal = strPtr(colB)
			}

			// Body: specification text + applicability lines.
			body := buildCCMBody(colD, cols)
			cr.Body = &body

			result.Controls = append(result.Controls, cr)
			ordinal++
			continue
		}

		// Otherwise: preamble continuation, empty row, or unrecognized — skip.
	}

	return result, nil
}

// buildCCMBody constructs the control body from the specification text (col D)
// and applicability columns. Format:
//
//	<specification text>
//
//	CCM Lite: No
//	IaaS: Shared
//	PaaS: Shared
//	SaaS: Shared
//	Phys: Yes
//	...
//
// Only non-empty applicability values are included. For architectural/
// organizational relevance columns (I-W), "True" is rendered as "Yes".
func buildCCMBody(spec string, cols map[string]string) string {
	var parts []string
	if spec != "" {
		parts = append(parts, spec)
	}

	var appLines []string
	for _, ac := range ccmApplicabilityCols {
		val := strings.TrimSpace(cols[ac.Col])
		if val == "" {
			continue
		}
		// Architectural/organizational relevance columns store "True" —
		// render as "Yes" for human readability.
		if val == "True" {
			val = "Yes"
		}
		appLines = append(appLines, ac.Label+": "+val)
	}

	if len(appLines) > 0 {
		if len(parts) > 0 {
			parts = append(parts, "") // blank line separator
		}
		parts = append(parts, strings.Join(appLines, "\n"))
	}

	return strings.Join(parts, "\n")
}
