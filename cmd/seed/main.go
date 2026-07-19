// Command seed loads compliary's framework registry and vocabularies from the
// embedded deploy/seed/*.csv into the config schema.
//
// It is re-runnable: each table's origin='seed' rows are deleted and reinserted
// from the CSV, while operator customizations (origin='user' rows) are
// preserved (the inserts skip rows that collide with a user override). Edit a
// CSV and re-run to refresh the shipped defaults.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	seed "danny.vn/compliary/deploy/seed"
	"danny.vn/compliary/pkg/base/config"
	"danny.vn/compliary/pkg/base/db"
	clog "danny.vn/compliary/pkg/base/log"
	dbconfig "danny.vn/compliary/pkg/store/config"
)

func main() {
	cfgPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	log := clog.New(os.Getenv("COMPLIARY_LOG_LEVEL"))
	if err := run(*cfgPath, log); err != nil {
		log.Error("seed", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string, log *slog.Logger) error {
	ctx := context.Background()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Info("compliary seed", "db", cfg.Database.Redacted())

	pool, err := db.NewPool(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	// One transaction so a partial CSV never leaves config half-seeded.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbconfig.New(tx)

	counts := map[string]int{}

	if err := q.DeleteSeedFrameworks(ctx); err != nil {
		return fmt.Errorf("clear framework seed: %w", err)
	}
	rows, err := readSeedCSV("framework.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		ingestEnabled, err := strconv.ParseBool(r[5])
		if err != nil {
			return fmt.Errorf("framework %q ingest_enabled: %w", r[0], err)
		}
		if err := q.InsertSeedFramework(ctx, dbconfig.InsertSeedFrameworkParams{
			Code: r[0], Name: r[1], Publisher: r[2], SourceAccess: r[3],
			LicenseClass: r[4], IngestEnabled: ingestEnabled, ServePolicy: r[6],
			CitationScheme: r[7], TermsNote: r[8],
		}); err != nil {
			return fmt.Errorf("insert framework %q: %w", r[0], err)
		}
	}
	counts["framework"] = len(rows)

	if err := q.DeleteSeedFrameworkVersions(ctx); err != nil {
		return fmt.Errorf("clear framework_version seed: %w", err)
	}
	rows, err = readSeedCSV("framework_version.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var publishedOn *time.Time
		if r[2] != "" {
			d, err := time.Parse("2006-01-02", r[2])
			if err != nil {
				return fmt.Errorf("framework_version %s/%s published_on: %w", r[0], r[1], err)
			}
			publishedOn = &d
		}
		isCurrent, err := strconv.ParseBool(r[3])
		if err != nil {
			return fmt.Errorf("framework_version %s/%s is_current: %w", r[0], r[1], err)
		}
		if err := q.InsertSeedFrameworkVersion(ctx, dbconfig.InsertSeedFrameworkVersionParams{
			FrameworkCode: r[0], VersionLabel: r[1], PublishedOn: publishedOn,
			IsCurrent: isCurrent, EditionNote: r[4],
		}); err != nil {
			return fmt.Errorf("insert framework_version %s/%s: %w", r[0], r[1], err)
		}
	}
	counts["framework_version"] = len(rows)

	if err := q.DeleteSeedMappingSources(ctx); err != nil {
		return fmt.Errorf("clear mapping_source seed: %w", err)
	}
	rows, err = readSeedCSV("mapping_source.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.InsertSeedMappingSource(ctx, dbconfig.InsertSeedMappingSourceParams{
			Code: r[0], Name: r[1], AuthorityNote: r[2],
		}); err != nil {
			return fmt.Errorf("insert mapping_source %q: %w", r[0], err)
		}
	}
	counts["mapping_source"] = len(rows)

	if err := q.DeleteSeedControlKinds(ctx); err != nil {
		return fmt.Errorf("clear control_kind seed: %w", err)
	}
	rows, err = readSeedCSV("control_kind.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.InsertSeedControlKind(ctx, dbconfig.InsertSeedControlKindParams{
			Code: r[0], Note: r[1],
		}); err != nil {
			return fmt.Errorf("insert control_kind %q: %w", r[0], err)
		}
	}
	counts["control_kind"] = len(rows)

	if err := q.DeleteSeedSettings(ctx); err != nil {
		return fmt.Errorf("clear setting seed: %w", err)
	}
	rows, err = readSeedCSV("setting.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.InsertSeedSetting(ctx, dbconfig.InsertSeedSettingParams{
			Key: r[0], Value: r[1],
		}); err != nil {
			return fmt.Errorf("insert setting %q: %w", r[0], err)
		}
	}
	counts["setting"] = len(rows)

	if err := q.DeleteSeedReferenceSources(ctx); err != nil {
		return fmt.Errorf("clear reference_source seed: %w", err)
	}
	rows, err = readSeedCSV("reference_source.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		enabled, err := strconv.ParseBool(r[4])
		if err != nil {
			return fmt.Errorf("reference_source %q enabled: %w", r[0], err)
		}
		if err := q.InsertSeedReferenceSource(ctx, dbconfig.InsertSeedReferenceSourceParams{
			Prefix:            r[0],
			ToFrameworkCode:   r[1],
			ToVersionLabel:    nullText(r[2]),
			MappingSourceCode: r[3],
			Enabled:           enabled,
		}); err != nil {
			return fmt.Errorf("insert reference_source %q: %w", r[0], err)
		}
	}
	counts["reference_source"] = len(rows)

	if err := q.DeleteSeedControlTitles(ctx); err != nil {
		return fmt.Errorf("clear control_title seed: %w", err)
	}
	rows, err = readSeedCSV("control_title.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := q.InsertSeedControlTitle(ctx, dbconfig.InsertSeedControlTitleParams{
			FrameworkCode: r[0], VersionLabel: r[1], CitationNorm: r[2], Title: r[3],
		}); err != nil {
			return fmt.Errorf("insert control_title %s/%s/%s: %w", r[0], r[1], r[2], err)
		}
	}
	counts["control_title"] = len(rows)

	if err := q.DeleteSeedFileRules(ctx); err != nil {
		return fmt.Errorf("clear file_rule seed: %w", err)
	}
	rows, err = readSeedCSV("file_rule.csv")
	if err != nil {
		return err
	}
	for _, r := range rows {
		ordinal, err := strconv.Atoi(r[0])
		if err != nil {
			return fmt.Errorf("file_rule %q ordinal: %w", r[1], err)
		}
		ignore, err := strconv.ParseBool(r[7])
		if err != nil {
			return fmt.Errorf("file_rule %q ignore: %w", r[1], err)
		}
		if err := q.InsertSeedFileRule(ctx, dbconfig.InsertSeedFileRuleParams{
			Ordinal:        int32(ordinal),
			Pattern:        r[1],
			FrameworkCode:  nullText(r[2]),
			VersionLabel:   nullText(r[3]),
			DocRole:        nullText(r[4]),
			Qualifier:      r[5],
			FileFormat:     nullText(r[6]),
			Ignore:         ignore,
			IgnoreReason:   r[8],
			LicenseKind:    nullText(r[9]),
			SourceUrl:      r[10],
			ProvenanceNote: r[11],
		}); err != nil {
			return fmt.Errorf("insert file_rule %q: %w", r[1], err)
		}
	}
	counts["file_rule"] = len(rows)

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	log.Info("seeded config",
		"framework", counts["framework"],
		"framework_version", counts["framework_version"],
		"mapping_source", counts["mapping_source"],
		"control_kind", counts["control_kind"],
		"setting", counts["setting"],
		"reference_source", counts["reference_source"],
		"control_title", counts["control_title"],
		"file_rule", counts["file_rule"],
	)
	return nil
}

// nullText returns a *string: nil for empty strings, pointer to the value otherwise.
func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// readSeedCSV reads an embedded seed CSV and returns its data rows with the
// header dropped. FieldsPerRecord stays at the header width, so a malformed row
// is rejected rather than silently widening the table.
func readSeedCSV(name string) ([][]string, error) {
	f, err := seed.FS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()

	recs, err := csv.NewReader(f).ReadAll()
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if len(recs) <= 1 {
		return nil, nil
	}
	return recs[1:], nil
}
