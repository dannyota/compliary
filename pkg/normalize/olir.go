// olir.go emits cross-framework mapping edges from NIST OLIR crosswalk
// workbooks captured in bronze. The first supported crosswalk is OLIR
// informative reference #155: SP 800-53 Rev 5 (focal 5.1.1) → ISO/IEC
// 27001:2022, developed by NIST.
//
// The workbook is one sheet per 800-53 family ("Relationships-AC", "AT", …)
// with columns A = focal element (zero-padded 800-53 citation, e.g. "AC-01"),
// D = reference element (ISO 27001 citation: bare clause "5.2" or Annex A
// "A.5.1"). This edition of the crosswalk carries no per-row relationship
// type, and NIST's own submission comments warn against assuming equivalency,
// so every edge is emitted as 'related' under mapping_source 'nist-olir'.
// provenance_detail records sheet + row only — never workbook text.
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

const mappingSourceOLIR = "nist-olir"

// olirISOTargetVersion pins emitted edges to the 27001 edition the crosswalk
// names. The resolution query then only matches that edition's controls.
const olirISOTargetVersion = "2022"

var reOLIRFocal = regexp.MustCompile(`^[A-Z]{2}-\d{2}(\(\d{2}\))?$`)
var reOLIRISORef = regexp.MustCompile(`^(A\.)?\d+(\.\d+)*$`)

// olirWorkbook mirrors the bronze workbook-rows-json capture shape.
type olirWorkbook struct {
	Sheets []olirSheet `json:"sheets"`
}

type olirSheet struct {
	Name string     `json:"name"`
	Rows []olirCell `json:"rows"`
}

type olirCell struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

// EmitOLIREdges parses the OLIR 800-53→27001 crosswalk capture (raw
// workbook-rows-json) and upserts one 'related' edge per (focal, reference)
// pair, from the existing nist80053 controls to iso27001:2022 citations.
// Focal citations that do not resolve to a stored control are counted and
// reported, never guessed.
func EmitOLIREdges(
	ctx context.Context,
	pool *pgxpool.Pool,
	silverQ MapEdgesQuerier,
	raw json.RawMessage,
	log *slog.Logger,
) (MapEdgesSummary, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	pairs, err := parseOLIRPairs(raw)
	if err != nil {
		return MapEdgesSummary{}, err
	}

	// Focal-side lookup: nist80053 control IDs by citation_norm.
	fromByNorm, err := loadControlIDsByNorm(ctx, pool, "nist80053")
	if err != nil {
		return MapEdgesSummary{}, err
	}

	var sum MapEdgesSummary
	var unknownFocal int
	for _, p := range pairs {
		fromID, ok := fromByNorm[p.FocalNorm]
		if !ok {
			unknownFocal++
			continue
		}
		ver := olirISOTargetVersion
		if err := silverQ.UpsertControlMapping(ctx, dbsilver.UpsertControlMappingParams{
			FromControlID:     fromID,
			ToFrameworkCode:   "iso27001",
			ToVersionLabel:    &ver,
			ToCitationNorm:    p.RefNorm,
			MappingSourceCode: mappingSourceOLIR,
			Relationship:      "related",
			ProvenanceDetail:  p.Provenance,
		}); err != nil {
			return sum, fmt.Errorf("olir: upsert %s→%s: %w", p.FocalNorm, p.RefNorm, err)
		}
		sum.Emitted++
	}

	if unknownFocal > 0 {
		log.Warn("olir: focal citations not found in nist80053 corpus", "count", unknownFocal)
	}

	resolved, err := silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return sum, fmt.Errorf("olir: resolve mappings: %w", err)
	}
	sum.Resolved = int(resolved)
	return sum, nil
}

// olirPair is one parsed (focal, reference) crosswalk row.
type olirPair struct {
	FocalNorm  string // e.g. "AC-01", "AC-02(01)"
	RefNorm    string // e.g. "5.2", "A.5.1"
	Provenance string // sheet + row reference only — never workbook text
}

var reOLIRCellRef = regexp.MustCompile(`^([A-Z]+)(\d+)$`)

// parseOLIRPairs extracts deduplicated crosswalk pairs from a
// workbook-rows-json capture. Pure function; header rows, blanks, and prose
// cells are skipped by the citation-shape gates.
func parseOLIRPairs(raw json.RawMessage) ([]olirPair, error) {
	var wb olirWorkbook
	if err := json.Unmarshal(raw, &wb); err != nil {
		return nil, fmt.Errorf("olir: unmarshal workbook: %w", err)
	}

	var out []olirPair
	seen := make(map[string]bool)
	for _, sh := range wb.Sheets {
		if sh.Name == "Definitions" {
			continue
		}
		// Reassemble rows: cells arrive flat with A1-style refs.
		rows := map[string]map[string]string{} // rowNum -> col -> value
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
			focal := strings.TrimSpace(cols["A"])
			ref := strings.TrimSpace(cols["D"])
			if !reOLIRFocal.MatchString(focal) || !reOLIRISORef.MatchString(ref) {
				continue
			}
			focalNorm := strings.ToUpper(focal)
			refNorm := strings.ToUpper(ref)
			dk := focalNorm + "→" + refNorm
			if seen[dk] {
				continue
			}
			seen[dk] = true
			out = append(out, olirPair{
				FocalNorm:  focalNorm,
				RefNorm:    refNorm,
				Provenance: fmt.Sprintf("sheet %s row %s", sh.Name, num),
			})
		}
	}
	return out, nil
}

// loadControlIDsByNorm maps citation_norm -> control ID for a framework's
// current-version main document.
func loadControlIDsByNorm(ctx context.Context, pool *pgxpool.Pool, frameworkCode string) (map[string]int64, error) {
	rows, err := pool.Query(ctx, `
SELECT sc.citation_norm, sc.id
FROM silver.control sc
JOIN silver.document d ON d.id = sc.document_id
JOIN config.framework_version fv
  ON fv.framework_code = d.framework_code AND fv.version_label = d.version_label
WHERE d.framework_code = $1 AND d.doc_role = 'main' AND fv.is_current`, frameworkCode)
	if err != nil {
		return nil, fmt.Errorf("olir: load %s controls: %w", frameworkCode, err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var norm string
		var id int64
		if err := rows.Scan(&norm, &id); err != nil {
			return nil, err
		}
		out[strings.ToUpper(norm)] = id
	}
	return out, rows.Err()
}
