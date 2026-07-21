package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	dbbronze "danny.vn/compliary/pkg/store/bronze"
	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
	dbsilver "danny.vn/compliary/pkg/store/silver"
)

// Summary holds the result counters from a normalize run.
type Summary struct {
	Succeeded int
	Failed    int
	Skipped   int
}

// IngestQuerier is the subset of dbingest.Querier needed by normalize.
type IngestQuerier interface {
	MarkNormalized(ctx context.Context, id int64) error
	SetStageError(ctx context.Context, arg dbingest.SetStageErrorParams) error
}

// BronzeQuerier is the subset of dbbronze.Querier needed by normalize.
type BronzeQuerier interface {
	GetSourceFile(ctx context.Context, arg dbbronze.GetSourceFileParams) (dbbronze.BronzeSourceFile, error)
	GetRawExtract(ctx context.Context, arg dbbronze.GetRawExtractParams) (dbbronze.BronzeRawExtract, error)
}

// SilverQuerier is the subset of dbsilver.Querier needed by normalize.
type SilverQuerier interface {
	UpsertDocument(ctx context.Context, arg dbsilver.UpsertDocumentParams) (dbsilver.SilverDocument, error)
	GetDocumentByKey(ctx context.Context, docKey string) (dbsilver.SilverDocument, error)
	DeleteControlsForDocument(ctx context.Context, documentID int64) (int64, error)
	InsertControl(ctx context.Context, arg dbsilver.InsertControlParams) (int64, error)
	UpsertControlMapping(ctx context.Context, arg dbsilver.UpsertControlMappingParams) error
	ResolveControlMappings(ctx context.Context) (int64, error)
}

// ConfigQuerier is the subset of dbconfig.Querier needed by normalize.
type ConfigQuerier interface {
	GetFramework(ctx context.Context, code string) (dbconfig.ConfigFramework, error)
	ListReferenceSources(ctx context.Context) ([]dbconfig.ConfigReferenceSource, error)
	ListControlTitles(ctx context.Context, arg dbconfig.ListControlTitlesParams) ([]dbconfig.ConfigControlTitle, error)
}

// DocIdentity carries the manifest-derived fields needed by writeTree.
type DocIdentity struct {
	ManifestID    int64
	RelPath       string
	Sha256        string
	FrameworkCode string
	VersionLabel  string
	DocRole       string
	Qualifier     string
	ServeGate     string // from bronze source_file
}

// Normalizer runs the normalize stage over a set of manifest rows.
type Normalizer struct {
	Log  *slog.Logger
	cfgQ ConfigQuerier // set during Run for title lookups
}

// Run processes the given manifest files (already filtered to normalize-eligible).
// It dispatches by the framework's citation_scheme — implemented: 'oscal-catalog',
// 'csf-workbook', 'cis-workbook', 'ccm-workbook', 'pci-requirement',
// 'tsc-criteria', 'cobit-objective', 'iso-ams', 'iso-control-catalog'; other
// schemes and non-'main' doc roles are skipped as deferrals.
func (n *Normalizer) Run(
	ctx context.Context,
	files []dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
	cfgQ ConfigQuerier,
) (Summary, error) {
	n.cfgQ = cfgQ
	var sum Summary

	for _, f := range files {
		fwCode := deref(f.FrameworkCode)
		if fwCode == "" {
			sum.Skipped++
			continue
		}

		// 'main' documents parse via the scheme switch below. Amendments parse
		// for the iso-ams scheme when the base main document is already in
		// silver — amendments attach to the base they modify, so an amendment
		// without its base (ISO 22301 Amd 1, base not acquired) stays
		// deferred. Companion workbooks (CAIQ) are deferred by design
		// (assessment questions are not controls).
		if role := deref(f.DocRole); role != "main" {
			if role == "amendment" {
				fw, err := cfgQ.GetFramework(ctx, fwCode)
				if err == nil && fw.CitationScheme == "iso-ams" {
					baseKey := fwCode + "|" + deref(f.VersionLabel) + "|main"
					if _, err := silverQ.GetDocumentByKey(ctx, baseKey); err == nil {
						if err := n.normalizeISOAmendment(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
							n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
							_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
								ID:         f.ID,
								StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
							})
							sum.Failed++
						} else {
							sum.Succeeded++
						}
						continue
					}
					n.Log.Info("normalize: deferred amendment (base document absent)", "path", f.RelPath, "base", baseKey)
					sum.Skipped++
					continue
				}
			}
			n.Log.Info("normalize: deferred doc_role", "path", f.RelPath, "doc_role", role)
			sum.Skipped++
			continue
		}

		// Look up the framework's citation_scheme.
		fw, err := cfgQ.GetFramework(ctx, fwCode)
		if err == nil && fw.TermsNote != "" {
			// Data-driven restricted-terms warning (e.g. AICPA's knowledge-base
			// clause): the registry carries the note; the operator owns the choice.
			n.Log.Warn("framework terms restriction", "code", fwCode, "terms_note", fw.TermsNote)
		}
		if err != nil {
			n.Log.Error("cannot load framework", "code", fwCode, "err", err)
			_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
				ID:         f.ID,
				StageError: fmt.Sprintf("normalize: cannot load framework %q: %s", fwCode, err.Error()),
			})
			sum.Failed++
			continue
		}

		switch fw.CitationScheme {
		case "oscal-catalog":
			if err := n.normalizeOSCAL(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "csf-workbook":
			if err := n.normalizeCSF(ctx, f, ingQ, bronzeQ, silverQ, cfgQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "cis-workbook":
			if err := n.normalizeCIS(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "ccm-workbook":
			if err := n.normalizeCCM(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "pci-requirement":
			if err := n.normalizePCI(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "tsc-criteria":
			if err := n.normalizeTSC(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "cobit-objective":
			if err := n.normalizeCOBIT(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "iso-ams":
			if err := n.normalizeISOAMS(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		case "iso-control-catalog":
			if err := n.normalizeISOControlCatalog(ctx, f, ingQ, bronzeQ, silverQ); err != nil {
				n.Log.Error("normalize failed", "path", f.RelPath, "err", err)
				_ = ingQ.SetStageError(ctx, dbingest.SetStageErrorParams{
					ID:         f.ID,
					StageError: fmt.Sprintf("normalize: %s: %s", f.RelPath, err.Error()),
				})
				sum.Failed++
			} else {
				sum.Succeeded++
			}
		default:
			// Unimplemented citation scheme — skip as deferral.
			sum.Skipped++
		}
	}

	if sum.Skipped > 0 {
		n.Log.Info("normalize: deferred schemes", "skipped", sum.Skipped)
	}

	return sum, nil
}

func (n *Normalizer) normalizeOSCAL(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract.
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "oscal-catalog-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the catalog into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildOSCALTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	// Log any unresolved links.
	for _, ul := range tree.UnresolvedLinks {
		n.Log.Warn("unresolved withdrawn link",
			"path", f.RelPath,
			"citation", ul.Citation,
			"href", ul.Href,
		)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeCSF(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
	cfgQ ConfigQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (workbook-rows-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "workbook-rows-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Load reference sources for informative-reference edge emission.
	dbRefs, err := cfgQ.ListReferenceSources(ctx)
	if err != nil {
		return fmt.Errorf("list reference sources: %w", err)
	}
	refSources := make([]ReferenceSource, len(dbRefs))
	for i, r := range dbRefs {
		refSources[i] = ReferenceSource{
			Prefix:            r.Prefix,
			ToFrameworkCode:   r.ToFrameworkCode,
			ToVersionLabel:    r.ToVersionLabel,
			MappingSourceCode: r.MappingSourceCode,
		}
	}

	// Parse the workbook into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildCSFTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel, refSources...)
	if err != nil {
		return fmt.Errorf("build CSF tree: %w", err)
	}

	// Log reference skip counts if any.
	if tree.RefSkips != nil {
		for pfx, count := range tree.RefSkips.PerPrefix {
			n.Log.Info("ref-skip", "prefix", pfx, "skips", count, "path", f.RelPath)
		}
		for pfx, count := range tree.RefSkips.UnknownPfx {
			n.Log.Info("ref-unknown-prefix", "prefix", pfx, "lines", count, "path", f.RelPath)
		}
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeCIS(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (workbook-rows-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "workbook-rows-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the workbook into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildCISTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build CIS tree: %w", err)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeCCM(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (workbook-rows-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "workbook-rows-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the workbook into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildCCMTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build CCM tree: %w", err)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizePCI(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (pdf-pages-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the capture into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildPCITree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build PCI tree: %w", err)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeTSC(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (pdf-pages-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the capture into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildTSCTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build TSC tree: %w", err)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeCOBIT(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	// Load source file from bronze.
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	// Load raw extract (pdf-pages-json capture).
	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	// Parse the capture into in-memory tree (pure function).
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildCOBITTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build COBIT tree: %w", err)
	}

	// Write tree to silver.
	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

// titleMap maps citation_norm → curated title for a specific framework/version.
type titleMap map[string]string

// loadTitleMap loads curated titles for the given framework+version from the
// config.control_title seed table. Returns nil if no titles are available.
func loadTitleMap(ctx context.Context, cfgQ ConfigQuerier, fwCode, verLabel string) (titleMap, error) {
	if cfgQ == nil {
		return nil, nil
	}
	rows, err := cfgQ.ListControlTitles(ctx, dbconfig.ListControlTitlesParams{
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("list control titles: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	m := make(titleMap, len(rows))
	for _, r := range rows {
		m[r.CitationNorm] = r.Title
	}
	return m, nil
}

// writeTree writes a TreeResult to silver: upsert document, delete+insert
// controls, upsert mapping edges, resolve mappings, mark normalized. This is
// the shared DB-writer extracted from normalizeOSCAL and normalizeCSF.
// Controls whose citation_norm has a curated title in config.control_title
// get it as silver.control.title (title_original stays unchanged).
func (n *Normalizer) writeTree(
	ctx context.Context,
	doc DocIdentity,
	tree *TreeResult,
	ingQ IngestQuerier,
	silverQ SilverQuerier,
) error {
	// Load curated titles for this framework/version.
	tm, err := loadTitleMap(ctx, n.cfgQ, doc.FrameworkCode, doc.VersionLabel)
	if err != nil {
		n.Log.Warn("curated titles unavailable, using parser titles",
			"framework", doc.FrameworkCode, "version", doc.VersionLabel, "err", err)
	}
	if tm != nil {
		n.Log.Info("applying curated titles",
			"framework", doc.FrameworkCode, "version", doc.VersionLabel, "count", len(tm))
	}
	// Build doc_key: <framework_code>|<version_label>|<doc_role>
	docKey := doc.FrameworkCode + "|" + doc.VersionLabel + "|" + doc.DocRole
	if doc.Qualifier != "" {
		docKey += ":" + doc.Qualifier
	}

	// Upsert silver.document.
	sdoc, err := silverQ.UpsertDocument(ctx, dbsilver.UpsertDocumentParams{
		DocKey:           docKey,
		FrameworkCode:    doc.FrameworkCode,
		VersionLabel:     doc.VersionLabel,
		DocRole:          doc.DocRole,
		Qualifier:        doc.Qualifier,
		Title:            tree.Title,
		SourceFileSha256: doc.Sha256,
		ServeGate:        doc.ServeGate,
		Markdown:         nil,
	})
	if err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// Delete existing controls (idempotent rebuild).
	_, err = silverQ.DeleteControlsForDocument(ctx, sdoc.ID)
	if err != nil {
		return fmt.Errorf("delete controls: %w", err)
	}

	// Pre-insert duplicate-citation check: a duplicate CitationNorm within
	// one document would violate the silver.control unique constraint and
	// surface only as a cryptic Postgres error. Catch it early with a
	// descriptive message.
	{
		seen := make(map[string]int, len(tree.Controls))
		for i, cr := range tree.Controls {
			if prev, dup := seen[cr.CitationNorm]; dup {
				return fmt.Errorf("duplicate citation_norm %q in document %s (indices %d and %d)",
					cr.CitationNorm, docKey, prev, i)
			}
			seen[cr.CitationNorm] = i
		}
	}

	// Insert all controls; track the index-to-DB-ID mapping for parent linking.
	dbIDs := make([]int64, len(tree.Controls))
	for i, cr := range tree.Controls {
		var parentID *int64
		if cr.ParentIdx >= 0 {
			pid := dbIDs[cr.ParentIdx]
			parentID = &pid
		}

		// Apply curated title when available (title_original stays unchanged).
		title := cr.Title
		if tm != nil {
			if curated, ok := tm[cr.CitationNorm]; ok {
				title = curated
			}
		}

		id, err := silverQ.InsertControl(ctx, dbsilver.InsertControlParams{
			DocumentID:         sdoc.ID,
			ParentControlID:    parentID,
			Citation:           cr.Citation,
			CitationNorm:       cr.CitationNorm,
			Kind:               cr.Kind,
			Status:             cr.Status,
			Title:              title,
			TitleOriginal:      cr.TitleOriginal,
			Body:               cr.Body,
			Ordinal:            cr.Ordinal,
			AmendsCitationNorm: cr.AmendsCitationNorm,
			AmendAction:        cr.AmendAction,
		})
		if err != nil {
			return fmt.Errorf("insert control %s: %w", cr.Citation, err)
		}
		dbIDs[i] = id
	}

	// Insert mapping edges.
	for _, m := range tree.Mappings {
		fromID := dbIDs[m.FromIdx]
		err := silverQ.UpsertControlMapping(ctx, dbsilver.UpsertControlMappingParams{
			FromControlID:     fromID,
			ToFrameworkCode:   m.ToFrameworkCode,
			ToVersionLabel:    m.ToVersionLabel,
			ToCitationNorm:    m.ToCitationNorm,
			MappingSourceCode: m.MappingSource,
			Relationship:      m.Relationship,
			ProvenanceDetail:  m.ProvenanceDetail,
		})
		if err != nil {
			return fmt.Errorf("upsert mapping from %s: %w", tree.Controls[m.FromIdx].Citation, err)
		}
	}

	// Resolve mapping edges.
	_, err = silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return fmt.Errorf("resolve mappings: %w", err)
	}

	// Mark the manifest row as normalized.
	if err := ingQ.MarkNormalized(ctx, doc.ManifestID); err != nil {
		return fmt.Errorf("mark normalized: %w", err)
	}

	n.Log.Info("normalized",
		"path", doc.RelPath,
		"controls", len(tree.Controls),
		"mappings", len(tree.Mappings),
	)
	return nil
}

func (n *Normalizer) normalizeISOAMS(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildISO27001Tree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build ISO AMS tree: %w", err)
	}

	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

// normalizeISOAmendment parses an ISO amendment document (doc_role
// 'amendment') into amendment rows attached to the base version. The caller
// has already verified the base main document exists in silver.
func (n *Normalizer) normalizeISOAmendment(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildISOAmendmentTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build ISO amendment tree: %w", err)
	}

	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func (n *Normalizer) normalizeISOControlCatalog(
	ctx context.Context,
	f dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
) error {
	sf, err := bronzeQ.GetSourceFile(ctx, dbbronze.GetSourceFileParams{
		ManifestRelPath: f.RelPath,
		Sha256:          f.Sha256,
	})
	if err != nil {
		return fmt.Errorf("get source_file: %w", err)
	}

	re, err := bronzeQ.GetRawExtract(ctx, dbbronze.GetRawExtractParams{
		SourceFileID: sf.ID,
		Kind:         "pdf-pages-json",
	})
	if err != nil {
		return fmt.Errorf("get raw_extract: %w", err)
	}

	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	tree, err := BuildISOControlCatalogTree(json.RawMessage(re.ContentJsonb), fwCode, verLabel)
	if err != nil {
		return fmt.Errorf("build ISO control catalog tree: %w", err)
	}

	doc := DocIdentity{
		ManifestID:    f.ID,
		RelPath:       f.RelPath,
		Sha256:        f.Sha256,
		FrameworkCode: fwCode,
		VersionLabel:  verLabel,
		DocRole:       deref(f.DocRole),
		Qualifier:     f.Qualifier,
		ServeGate:     sf.ServeGate,
	}
	return n.writeTree(ctx, doc, tree, ingQ, silverQ)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
