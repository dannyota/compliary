package normalize

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// BuildISOAmendmentTree parses a pdf-pages-json capture of an ISO amendment
// document (e.g. ISO/IEC 27001:2022/Amd 1:2024) into amendment rows. ISO
// amendments are instruction lists: a bare clause citation on its own line,
// followed by an editing instruction ("Add the following sentence at the end
// of the subclause:", "Replace ... with:", "Delete ..."), followed by the
// amended text. Each instruction becomes one ControlRow with
// AmendsCitationNorm = the target clause and AmendAction = add/replace/delete.
//
// Titles are generated neutral labels (no licensed text); the instruction and
// amended text land in Body (auth-gated like every verbatim field). This is a
// pure function with no side effects.
func BuildISOAmendmentTree(raw json.RawMessage, frameworkCode, versionLabel string) (*TreeResult, error) {
	var cap isoCapture
	if err := json.Unmarshal(raw, &cap); err != nil {
		return nil, fmt.Errorf("unmarshal pdf capture: %w", err)
	}

	items := parseISOAmendmentInstructions(cap.Pages)
	if len(items) == 0 {
		return nil, fmt.Errorf("no amendment instructions found in capture")
	}

	tree := &TreeResult{
		Title: fmt.Sprintf("%s %s amendment", isoFrameworkDisplayName(frameworkCode), versionLabel),
	}
	for i, it := range items {
		action := it.action
		amends := it.citation // ISO amendment targets cite the base clause verbatim
		body := it.body
		tree.Controls = append(tree.Controls, ControlRow{
			Citation:           it.citation,
			CitationNorm:       strings.ToUpper(it.citation),
			Kind:               "clause",
			Status:             "active",
			Title:              fmt.Sprintf("Amendment change to clause %s", it.citation),
			Body:               &body,
			ParentIdx:          -1,
			Ordinal:            int32(i),
			AmendsCitationNorm: &amends,
			AmendAction:        &action,
		})
	}
	return tree, nil
}

// isoAmdInstruction is one parsed editing instruction.
type isoAmdInstruction struct {
	citation string // base clause being amended, e.g. "4.1"
	action   string // add | replace | delete
	body     string // instruction line + amended text
}

// reISOAmdCitation matches a bare clause citation line (e.g. "4.1", "A.5.7").
var reISOAmdCitation = regexp.MustCompile(`^(A\.)?\d+(\.\d+)*$`)

// reISOAmdInstruction matches the editing-instruction line that follows a
// citation and yields the action verb.
var reISOAmdInstruction = regexp.MustCompile(`(?i)^(Add|Replace|Delete)\b`)

// parseISOAmendmentInstructions scans page texts for citation → instruction →
// text runs. Front matter (title page, copyright, foreword) never contains a
// bare clause citation line directly followed by an instruction line, so the
// pattern gate is sufficient to skip it.
func parseISOAmendmentInstructions(pages []isoPage) []isoAmdInstruction {
	var out []isoAmdInstruction

	for _, pg := range pages {
		lines := strings.Split(pg.Text, "\n")
		for i := 0; i < len(lines); i++ {
			cite := strings.TrimSpace(lines[i])
			if !reISOAmdCitation.MatchString(cite) || i+1 >= len(lines) {
				continue
			}
			instr := strings.TrimSpace(lines[i+1])
			m := reISOAmdInstruction.FindStringSubmatch(instr)
			if m == nil {
				continue
			}

			// Collect the instruction and the amended text until the next
			// citation+instruction pair or an end-of-content marker.
			parts := []string{instr}
			j := i + 2
			for ; j < len(lines); j++ {
				txt := strings.TrimSpace(lines[j])
				if txt == "" {
					continue
				}
				// Stop at the next instruction target.
				if reISOAmdCitation.MatchString(txt) && j+1 < len(lines) &&
					reISOAmdInstruction.MatchString(strings.TrimSpace(lines[j+1])) {
					break
				}
				// Stop at page footer artifacts (page number / copyright).
				if regexp.MustCompile(`^\d+$`).MatchString(txt) || strings.Contains(txt, "All rights reserved") {
					continue
				}
				parts = append(parts, txt)
			}

			out = append(out, isoAmdInstruction{
				citation: cite,
				action:   strings.ToLower(m[1]),
				body:     strings.Join(parts, "\n"),
			})
			i = j - 1
		}
	}
	return out
}

// isoFrameworkDisplayName renders a neutral document title for ISO frameworks.
func isoFrameworkDisplayName(frameworkCode string) string {
	switch frameworkCode {
	case "iso27001":
		return "ISO/IEC 27001"
	case "iso22301":
		return "ISO 22301"
	default:
		return frameworkCode
	}
}
