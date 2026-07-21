// cismap.go emits cross-framework mapping edges from the CIS Controls v8.1
// mapping workbooks (CIS-published, CC BY-NC-ND — same click-through as the
// Controls workbook). Three targets are supported: ISO/IEC 27001:2022,
// NIST CSF 2.0, and NIST SP 800-53 r5.
//
// Workbook shape (sheet "All CIS Controls & Safeguards", header row 1):
// C = CIS Safeguard number, F = Safeguard title, K = Relationship
// (Equivalent / Subset / Superset), L = target citation. Column C is stored as
// a FLOAT by the publisher, which merges safeguards N.1 and N.10 into the
// same value (4.1 == 4.10) — so safeguards are resolved by TITLE against the
// ciscontrols corpus instead of by number. Rows whose title doesn't match a
// stored safeguard are counted and reported, never guessed.
//
// Relationship semantics follow the sheet's focal→reference direction: a CIS
// safeguard that is a "Subset" of the target becomes relationship 'subset-of'
// from the safeguard to the target.
package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	dbsilver "danny.vn/compliary/pkg/store/silver"
)

const mappingSourceCIS = "cis-v8.1-mappings"

// CISMappingSpec describes one CIS mapping workbook: where its capture lives
// and how to normalize its target citations.
type CISMappingSpec struct {
	RelPath       string // manifest rel_path of the workbook
	ToFramework   string
	ToVersion     string
	NormalizeCite func(string) (string, bool) // raw L value -> citation_norm, ok
}

// CISMappingSpecs lists the supported workbooks. Absent files are skipped by
// the caller (deferral, not error).
var CISMappingSpecs = []CISMappingSpec{
	{
		RelPath:       "cis/cis-controls-v8.1-mapping-to-iso-iec-27001-2022.xlsx",
		ToFramework:   "iso27001",
		ToVersion:     "2022",
		NormalizeCite: normalizeCISISOTarget,
	},
	{
		RelPath:       "cis/cis-controls-v8.1-mapping-to-nist-csf-2.0.xlsx",
		ToFramework:   "nistcsf",
		ToVersion:     "2.0",
		NormalizeCite: normalizeCISCSFTarget,
	},
	{
		RelPath:       "cis/cis-controls-v8.1-mapping-to-nist-sp-800-53-r5.xlsx",
		ToFramework:   "nist80053",
		ToVersion:     "r5",
		NormalizeCite: normalizeCIS80053Target,
	},
}

var (
	reCISISOAnnex  = regexp.MustCompile(`^A(\d+)\.(\d+)$`)            // "A5.9" -> A.5.9
	reCISISOClause = regexp.MustCompile(`^\d+(\.\d+)*$`)              // "7.5.1"
	reCISCSF       = regexp.MustCompile(`^[A-Z]{2}\.[A-Z]{2}-\d{2}$`) // "ID.AM-01"
	reCIS80053     = regexp.MustCompile(`^([A-Z]{2})-(\d+)(?:\((\d+)\))?$`)
)

func normalizeCISISOTarget(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if m := reCISISOAnnex.FindStringSubmatch(s); m != nil {
		return "A." + m[1] + "." + m[2], true
	}
	if reCISISOClause.MatchString(s) {
		return s, true
	}
	return "", false
}

func normalizeCISCSFTarget(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !reCISCSF.MatchString(s) {
		return "", false
	}
	return s, true
}

func normalizeCIS80053Target(s string) (string, bool) {
	m := reCIS80053.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return "", false
	}
	out := m[1] + "-" + pad2(m[2])
	if m[3] != "" {
		out += "(" + pad2(m[3]) + ")"
	}
	return out, true
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// cisRelationships maps the workbook's Relationship column to the
// silver.control_mapping vocabulary. Unknown values are skipped and counted.
var cisRelationships = map[string]string{
	"equivalent": "equivalent",
	"subset":     "subset-of",
	"superset":   "superset-of",
}

// cisMapPair is one parsed workbook row.
type cisMapPair struct {
	SafeguardTitle string // col F — resolves the safeguard (col C is float-mangled)
	Relationship   string // already vocabulary-mapped
	TargetNorm     string // normalized target citation
	Provenance     string // sheet + row only
}

// parseCISMappingPairs extracts mapping rows from a workbook-rows-json
// capture. Pure function. Rows without a relationship or a target are the
// safeguard-definition rows and are skipped silently; rows with an unknown
// relationship or unparseable target are returned in the skipped count.
func parseCISMappingPairs(raw json.RawMessage, normalize func(string) (string, bool)) (pairs []cisMapPair, skipped int, err error) {
	var wb olirWorkbook // same capture shape: sheets -> ref/value cells
	if err := json.Unmarshal(raw, &wb); err != nil {
		return nil, 0, fmt.Errorf("cismap: unmarshal workbook: %w", err)
	}

	for _, sh := range wb.Sheets {
		if !strings.HasPrefix(sh.Name, "All CIS") {
			continue
		}
		rows := map[string]map[string]string{}
		for _, c := range sh.Rows {
			m := reOLIRCellRef.FindStringSubmatch(c.Ref)
			if m == nil {
				continue
			}
			if rows[m[2]] == nil {
				rows[m[2]] = map[string]string{}
			}
			rows[m[2]][m[1]] = c.Value
		}
		for num, cols := range rows {
			rel := strings.ToLower(strings.TrimSpace(cols["K"]))
			target := strings.TrimSpace(cols["L"])
			title := strings.TrimSpace(cols["F"])
			if rel == "" && target == "" {
				continue // control/safeguard definition row, header, blank
			}
			if rel == "relationship" {
				continue // header row
			}
			mapped, ok := cisRelationships[rel]
			if !ok {
				skipped++
				continue
			}
			norm, ok := normalize(target)
			if !ok || title == "" {
				skipped++
				continue
			}
			pairs = append(pairs, cisMapPair{
				SafeguardTitle: title,
				Relationship:   mapped,
				TargetNorm:     norm,
				Provenance:     fmt.Sprintf("sheet %s row %s", sh.Name, num),
			})
		}
	}
	return pairs, skipped, nil
}

// EmitCISMappingEdges parses one CIS mapping workbook capture and upserts its
// edges from ciscontrols safeguards (resolved by title) to the spec's target
// framework.
func EmitCISMappingEdges(
	ctx context.Context,
	pool *pgxpool.Pool,
	silverQ MapEdgesQuerier,
	spec CISMappingSpec,
	raw json.RawMessage,
	log *slog.Logger,
) (MapEdgesSummary, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	pairs, skipped, err := parseCISMappingPairs(raw, spec.NormalizeCite)
	if err != nil {
		return MapEdgesSummary{}, err
	}
	if skipped > 0 {
		log.Warn("cismap: rows skipped (unknown relationship or unparseable target)",
			"file", spec.RelPath, "skipped", skipped)
	}

	titleToID, err := loadCISSafeguardsByTitle(ctx, pool)
	if err != nil {
		return MapEdgesSummary{}, err
	}

	var sum MapEdgesSummary
	var unknownTitle int
	seen := map[string]bool{}
	for _, p := range pairs {
		fromID, ok := titleToID[normTitleKey(p.SafeguardTitle)]
		if !ok {
			unknownTitle++
			continue
		}
		dk := fmt.Sprintf("%d→%s→%s", fromID, p.TargetNorm, p.Relationship)
		if seen[dk] {
			continue
		}
		seen[dk] = true

		ver := spec.ToVersion
		if err := silverQ.UpsertControlMapping(ctx, dbsilver.UpsertControlMappingParams{
			FromControlID:     fromID,
			ToFrameworkCode:   spec.ToFramework,
			ToVersionLabel:    &ver,
			ToCitationNorm:    p.TargetNorm,
			MappingSourceCode: mappingSourceCIS,
			Relationship:      p.Relationship,
			ProvenanceDetail:  p.Provenance,
		}); err != nil {
			return sum, fmt.Errorf("cismap: upsert →%s: %w", p.TargetNorm, err)
		}
		sum.Emitted++
	}
	if unknownTitle > 0 {
		log.Warn("cismap: safeguard titles not found in ciscontrols corpus",
			"file", spec.RelPath, "count", unknownTitle)
	}

	resolved, err := silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return sum, fmt.Errorf("cismap: resolve mappings: %w", err)
	}
	sum.Resolved = int(resolved)
	return sum, nil
}

// loadCISSafeguardsByTitle maps whitespace/case-normalized safeguard titles to
// control IDs for the current ciscontrols version. Titles come from the same
// CIS publication, so they match byte-for-byte modulo whitespace.
func loadCISSafeguardsByTitle(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `
SELECT sc.title, COALESCE(sc.title_original, ''), sc.id
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code AND fv.version_label = d.version_label
WHERE d.framework_code = 'ciscontrols' AND d.doc_role = 'main' AND fv.is_current`)
	if err != nil {
		return nil, fmt.Errorf("cismap: load ciscontrols safeguards: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var title, orig string
		var id int64
		if err := rows.Scan(&title, &orig, &id); err != nil {
			return nil, err
		}
		if title != "" {
			out[normTitleKey(title)] = id
		}
		if orig != "" {
			out[normTitleKey(orig)] = id
		}
	}
	return out, rows.Err()
}

var reSpaces = regexp.MustCompile(`\s+`)

// normTitleKey normalizes a title for matching: lowercase, collapsed spaces.
func normTitleKey(s string) string {
	return reSpaces.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
}
