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
	DeleteControlsForDocument(ctx context.Context, documentID int64) (int64, error)
	InsertControl(ctx context.Context, arg dbsilver.InsertControlParams) (int64, error)
	UpsertControlMapping(ctx context.Context, arg dbsilver.UpsertControlMappingParams) error
	ResolveControlMappings(ctx context.Context) (int64, error)
}

// ConfigQuerier is the subset of dbconfig.Querier needed by normalize.
type ConfigQuerier interface {
	GetFramework(ctx context.Context, code string) (dbconfig.ConfigFramework, error)
	ListReferenceSources(ctx context.Context) ([]dbconfig.ConfigReferenceSource, error)
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
	Log *slog.Logger
}

// Run processes the given manifest files (already filtered to normalize-eligible).
// It dispatches by the framework's citation_scheme: only 'oscal-catalog' is
// implemented; others are skipped as deferrals.
func (n *Normalizer) Run(
	ctx context.Context,
	files []dbingest.IngestManifestFile,
	ingQ IngestQuerier,
	bronzeQ BronzeQuerier,
	silverQ SilverQuerier,
	cfgQ ConfigQuerier,
) (Summary, error) {
	var sum Summary

	for _, f := range files {
		fwCode := deref(f.FrameworkCode)
		if fwCode == "" {
			sum.Skipped++
			continue
		}

		// Only 'main' documents parse today. Companion workbooks (CAIQ) and
		// amendments are deferred until their parsers land — a deferral, not
		// an error.
		if deref(f.DocRole) != "main" {
			n.Log.Info("normalize: deferred doc_role", "path", f.RelPath, "doc_role", deref(f.DocRole))
			sum.Skipped++
			continue
		}

		// Look up the framework's citation_scheme.
		fw, err := cfgQ.GetFramework(ctx, fwCode)
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

// writeTree writes a TreeResult to silver: upsert document, delete+insert
// controls, upsert mapping edges, resolve mappings, mark normalized. This is
// the shared DB-writer extracted from normalizeOSCAL and normalizeCSF.
func (n *Normalizer) writeTree(
	ctx context.Context,
	doc DocIdentity,
	tree *TreeResult,
	ingQ IngestQuerier,
	silverQ SilverQuerier,
) error {
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

	// Insert all controls; track the index-to-DB-ID mapping for parent linking.
	dbIDs := make([]int64, len(tree.Controls))
	for i, cr := range tree.Controls {
		var parentID *int64
		if cr.ParentIdx >= 0 {
			pid := dbIDs[cr.ParentIdx]
			parentID = &pid
		}

		id, err := silverQ.InsertControl(ctx, dbsilver.InsertControlParams{
			DocumentID:      sdoc.ID,
			ParentControlID: parentID,
			Citation:        cr.Citation,
			CitationNorm:    cr.CitationNorm,
			Kind:            cr.Kind,
			Status:          cr.Status,
			Title:           cr.Title,
			TitleOriginal:   cr.TitleOriginal,
			Body:            cr.Body,
			Ordinal:         cr.Ordinal,
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
