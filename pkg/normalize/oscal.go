// Package normalize implements the normalize pipeline stage: read
// extracted rows from bronze, dispatch by the framework's citation_scheme,
// build the silver control tree, and write to silver.document + silver.control
// + silver.control_mapping.
package normalize

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ControlRow is an in-memory representation of a silver.control row,
// produced by the pure tree builder before any DB writes.
type ControlRow struct {
	Citation      string
	CitationNorm  string
	Kind          string // family, control, enhancement
	Status        string // active, withdrawn
	Title         string
	TitleOriginal *string
	Body          *string
	ParentIdx     int // index into the parent ControlRow slice; -1 for top-level
	Ordinal       int32
}

// MappingEdge is an in-memory representation of a silver.control_mapping row.
type MappingEdge struct {
	FromIdx          int    // index into ControlRow slice (the withdrawn control)
	ToFrameworkCode  string // e.g. "nist80053"
	ToVersionLabel   *string
	ToCitationNorm   string
	MappingSource    string // e.g. "publisher-catalog"
	Relationship     string // e.g. "incorporated-into", "moved-to"
	ProvenanceDetail string // the OSCAL link href
}

// UnresolvedLink records a withdrawn-control link whose target could not be
// resolved to a citation in the catalog.
type UnresolvedLink struct {
	Citation string // the withdrawn control's citation
	Href     string // the raw OSCAL link href
}

// TreeResult holds the output of the pure OSCAL tree builder.
type TreeResult struct {
	Title           string // catalog title from metadata
	Controls        []ControlRow
	Mappings        []MappingEdge
	UnresolvedLinks []UnresolvedLink
}

// BuildOSCALTree parses an OSCAL catalog JSON document and returns the
// normalized control tree and mapping edges as pure data. This is deterministic
// and has no side effects — tests assert on the output directly.
func BuildOSCALTree(raw json.RawMessage) (*TreeResult, error) {
	var cat struct {
		Catalog struct {
			Metadata struct {
				Title   string `json:"title"`
				Version string `json:"version"`
			} `json:"metadata"`
			Groups []oscalGroup `json:"groups"`
			Params []oscalParam `json:"params"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("unmarshal catalog: %w", err)
	}
	if cat.Catalog.Metadata.Title == "" {
		return nil, fmt.Errorf("missing catalog.metadata.title")
	}
	if cat.Catalog.Groups == nil {
		return nil, fmt.Errorf("missing catalog.groups")
	}

	result := &TreeResult{
		Title: cat.Catalog.Metadata.Title,
	}

	// Build param index (global catalog-level params first).
	paramIdx := make(map[string]oscalParam)
	for _, p := range cat.Catalog.Params {
		paramIdx[p.ID] = p
	}

	// Build id→label index for link resolution (groups + controls).
	idToLabel := make(map[string]string)
	for _, g := range cat.Catalog.Groups {
		idToLabel[g.ID] = groupLabel(g)
		buildIDIndex(g.Controls, idToLabel)
	}

	var ordinal int32
	for _, g := range cat.Catalog.Groups {
		// Family row.
		familyLabel := groupLabel(g)
		familyIdx := len(result.Controls)
		result.Controls = append(result.Controls, ControlRow{
			Citation:      familyLabel,
			CitationNorm:  strings.ToUpper(strings.ReplaceAll(familyLabel, " ", "")),
			Kind:          "family",
			Status:        "active",
			Title:         g.Title,
			TitleOriginal: strPtr(g.Title),
			ParentIdx:     -1,
			Ordinal:       ordinal,
		})
		ordinal++

		// Build local param index for each group's controls.
		for _, c := range g.Controls {
			addControlParams(c, paramIdx)
		}

		// Controls.
		for _, c := range g.Controls {
			controlIdx := len(result.Controls)
			label := controlLabel(c)
			status := controlStatus(c)
			body := buildBody(c, paramIdx)

			result.Controls = append(result.Controls, ControlRow{
				Citation:      label,
				CitationNorm:  strings.ToUpper(strings.ReplaceAll(label, " ", "")),
				Kind:          "control",
				Status:        status,
				Title:         c.Title,
				TitleOriginal: strPtr(c.Title),
				Body:          body,
				ParentIdx:     familyIdx,
				Ordinal:       ordinal,
			})
			ordinal++

			// Withdrawn links.
			if status == "withdrawn" {
				for _, link := range c.Links {
					if link.Rel == "incorporated-into" || link.Rel == "moved-to" {
						targetNorm := resolveHref(link.Href, idToLabel)
						if targetNorm == "" {
							result.UnresolvedLinks = append(result.UnresolvedLinks, UnresolvedLink{
								Citation: label,
								Href:     link.Href,
							})
							continue
						}
						result.Mappings = append(result.Mappings, MappingEdge{
							FromIdx:          controlIdx,
							ToFrameworkCode:  "nist80053",
							ToVersionLabel:   strPtr("r5"),
							ToCitationNorm:   targetNorm,
							MappingSource:    "publisher-catalog",
							Relationship:     link.Rel,
							ProvenanceDetail: link.Href,
						})
					}
				}
			}

			// Enhancements (nested controls).
			for _, enh := range c.Controls {
				addControlParams(enh, paramIdx)
				enhIdx := len(result.Controls)
				enhLabel := controlLabel(enh)
				enhStatus := controlStatus(enh)
				enhBody := buildBody(enh, paramIdx)

				result.Controls = append(result.Controls, ControlRow{
					Citation:      enhLabel,
					CitationNorm:  strings.ToUpper(strings.ReplaceAll(enhLabel, " ", "")),
					Kind:          "enhancement",
					Status:        enhStatus,
					Title:         enh.Title,
					TitleOriginal: strPtr(enh.Title),
					Body:          enhBody,
					ParentIdx:     controlIdx,
					Ordinal:       ordinal,
				})
				ordinal++

				// Withdrawn links on enhancements.
				if enhStatus == "withdrawn" {
					for _, link := range enh.Links {
						if link.Rel == "incorporated-into" || link.Rel == "moved-to" {
							targetNorm := resolveHref(link.Href, idToLabel)
							if targetNorm == "" {
								result.UnresolvedLinks = append(result.UnresolvedLinks, UnresolvedLink{
									Citation: enhLabel,
									Href:     link.Href,
								})
								continue
							}
							result.Mappings = append(result.Mappings, MappingEdge{
								FromIdx:          enhIdx,
								ToFrameworkCode:  "nist80053",
								ToVersionLabel:   strPtr("r5"),
								ToCitationNorm:   targetNorm,
								MappingSource:    "publisher-catalog",
								Relationship:     link.Rel,
								ProvenanceDetail: link.Href,
							})
						}
					}
				}
			}
		}
	}

	return result, nil
}

// --- OSCAL data types ---

type oscalGroup struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Props    []oscalProp    `json:"props"`
	Controls []oscalControl `json:"controls"`
}

type oscalControl struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Props    []oscalProp    `json:"props"`
	Params   []oscalParam   `json:"params"`
	Parts    []oscalPart    `json:"parts"`
	Links    []oscalLink    `json:"links"`
	Controls []oscalControl `json:"controls"`
}

type oscalProp struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type oscalParam struct {
	ID     string       `json:"id"`
	Label  string       `json:"label"`
	Select *oscalSelect `json:"select"`
}

type oscalSelect struct {
	HowMany string   `json:"how-many"`
	Choice  []string `json:"choice"`
}

type oscalPart struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Prose string      `json:"prose"`
	Parts []oscalPart `json:"parts"`
}

type oscalLink struct {
	Href string `json:"href"`
	Rel  string `json:"rel"`
}

// --- Helpers ---

func groupLabel(g oscalGroup) string {
	for _, p := range g.Props {
		if p.Name == "label" {
			return p.Value
		}
	}
	// Fallback: uppercase id.
	return strings.ToUpper(g.ID)
}

func controlLabel(c oscalControl) string {
	for _, p := range c.Props {
		if p.Name == "label" {
			return p.Value
		}
	}
	// Fallback: uppercase id.
	return strings.ToUpper(c.ID)
}

func controlStatus(c oscalControl) string {
	for _, p := range c.Props {
		if p.Name == "status" && p.Value == "withdrawn" {
			return "withdrawn"
		}
	}
	return "active"
}

// buildIDIndex recursively populates the id→label map for all controls.
func buildIDIndex(controls []oscalControl, idx map[string]string) {
	for _, c := range controls {
		label := controlLabel(c)
		idx[c.ID] = label
		buildIDIndex(c.Controls, idx)
	}
}

// addControlParams registers a control's params in the global param index.
func addControlParams(c oscalControl, idx map[string]oscalParam) {
	for _, p := range c.Params {
		idx[p.ID] = p
	}
	for _, sub := range c.Controls {
		addControlParams(sub, idx)
	}
}

// resolveHref converts an OSCAL link href (#control-id or #control-id_part)
// to the target control's citation_norm.
func resolveHref(href string, idToLabel map[string]string) string {
	// Strip leading '#'.
	target := strings.TrimPrefix(href, "#")
	// If it contains '_', it's a part reference — take the control id part.
	if idx := strings.Index(target, "_"); idx > 0 {
		target = target[:idx]
	}
	label, ok := idToLabel[target]
	if !ok {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(label, " ", ""))
}

// buildBody flattens statement and guidance parts into plain text with
// param inserts rendered.
func buildBody(c oscalControl, paramIdx map[string]oscalParam) *string {
	var parts []string

	for _, p := range c.Parts {
		if p.Name == "statement" || p.Name == "guidance" {
			text := flattenPart(p, paramIdx, "")
			if text != "" {
				parts = append(parts, text)
			}
		}
	}

	if len(parts) == 0 {
		return nil
	}
	body := strings.Join(parts, "\n\n")
	return &body
}

// flattenPart recursively flattens a part tree into plain text, rendering
// param inserts inline.
func flattenPart(p oscalPart, paramIdx map[string]oscalParam, indent string) string {
	var lines []string

	if p.Prose != "" {
		rendered := renderInserts(p.Prose, paramIdx)
		lines = append(lines, indent+rendered)
	}

	for _, sub := range p.Parts {
		subText := flattenPart(sub, paramIdx, indent+"  ")
		if subText != "" {
			lines = append(lines, subText)
		}
	}

	return strings.Join(lines, "\n")
}

// renderInserts replaces {{ insert: param, <id> }} with [Assignment: <label>]
// or [Selection: a; b] based on the param definition.
func renderInserts(prose string, paramIdx map[string]oscalParam) string {
	// Find and replace all {{ insert: param, <id> }} patterns.
	result := prose
	for {
		start := strings.Index(result, "{{ insert: param, ")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], " }}")
		if end < 0 {
			break
		}
		end += start + 3 // include " }}"

		// Extract param id.
		inner := result[start+len("{{ insert: param, ") : end-3]
		inner = strings.TrimSpace(inner)

		// Look up param.
		replacement := renderParam(inner, paramIdx)
		result = result[:start] + replacement + result[end:]
	}

	return result
}

// renderParam renders a single param reference as [Assignment: <label>] or
// [Selection: choice1; choice2].
func renderParam(paramID string, paramIdx map[string]oscalParam) string {
	p, ok := paramIdx[paramID]
	if !ok {
		return "[Assignment: " + paramID + "]"
	}

	if p.Select != nil && len(p.Select.Choice) > 0 {
		if p.Select.HowMany == "one-or-more" {
			return "[Selection (one or more): " + strings.Join(p.Select.Choice, "; ") + "]"
		}
		return "[Selection: " + strings.Join(p.Select.Choice, "; ") + "]"
	}

	if p.Label != "" {
		return "[Assignment: " + p.Label + "]"
	}

	return "[Assignment: " + paramID + "]"
}

func strPtr(s string) *string { return &s }
