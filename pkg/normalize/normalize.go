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
	tree, err := BuildOSCALTree(json.RawMessage(re.ContentJsonb))
	if err != nil {
		return fmt.Errorf("build tree: %w", err)
	}

	// Build doc_key: <framework_code>|<version_label>|<doc_role>
	fwCode := deref(f.FrameworkCode)
	verLabel := deref(f.VersionLabel)
	docRole := deref(f.DocRole)
	qualifier := f.Qualifier
	docKey := fwCode + "|" + verLabel + "|" + docRole
	if qualifier != "" {
		docKey += ":" + qualifier
	}

	// Upsert silver.document.
	doc, err := silverQ.UpsertDocument(ctx, dbsilver.UpsertDocumentParams{
		DocKey:           docKey,
		FrameworkCode:    fwCode,
		VersionLabel:     verLabel,
		DocRole:          docRole,
		Qualifier:        qualifier,
		Title:            tree.Title,
		SourceFileSha256: f.Sha256,
		ServeGate:        sf.ServeGate,
		Markdown:         nil,
	})
	if err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// Delete existing controls (idempotent rebuild).
	_, err = silverQ.DeleteControlsForDocument(ctx, doc.ID)
	if err != nil {
		return fmt.Errorf("delete controls: %w", err)
	}

	// Insert all controls; track the index→DB ID mapping for parent linking.
	dbIDs := make([]int64, len(tree.Controls))
	for i, cr := range tree.Controls {
		var parentID *int64
		if cr.ParentIdx >= 0 {
			pid := dbIDs[cr.ParentIdx]
			parentID = &pid
		}

		id, err := silverQ.InsertControl(ctx, dbsilver.InsertControlParams{
			DocumentID:      doc.ID,
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

	// Resolve intra-document mapping edges.
	_, err = silverQ.ResolveControlMappings(ctx)
	if err != nil {
		return fmt.Errorf("resolve mappings: %w", err)
	}

	// Mark the manifest row as normalized.
	if err := ingQ.MarkNormalized(ctx, f.ID); err != nil {
		return fmt.Errorf("mark normalized: %w", err)
	}

	n.Log.Info("normalized",
		"path", f.RelPath,
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
