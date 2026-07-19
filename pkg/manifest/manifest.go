// Package manifest implements the manifest pipeline stage: walk data/,
// sha256+size each file, match against config.file_rule, upsert into
// ingest.manifest_file, demote missing paths, and detect parse-eligible
// ambiguity duplicates.
package manifest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dbconfig "danny.vn/compliary/pkg/store/config"
	dbingest "danny.vn/compliary/pkg/store/ingest"
)

// Rule is a file_rule row loaded from config. Rules are matched by ordinal
// (first match wins) using path.Match semantics on the file's rel_path.
type Rule struct {
	Ordinal       int32
	Pattern       string
	FrameworkCode *string
	VersionLabel  *string
	DocRole       *string
	Qualifier     string
	FileFormat    *string
	Ignore        bool
	IgnoreReason  string
	LicenseKind   *string
	SourceURL     string
	Provenance    string
}

// RulesFromDB converts database file_rule rows into Rules (already ordered).
func RulesFromDB(rows []dbconfig.ConfigFileRule) []Rule {
	rules := make([]Rule, len(rows))
	for i, r := range rows {
		rules[i] = Rule{
			Ordinal:       r.Ordinal,
			Pattern:       r.Pattern,
			FrameworkCode: r.FrameworkCode,
			VersionLabel:  r.VersionLabel,
			DocRole:       r.DocRole,
			Qualifier:     r.Qualifier,
			FileFormat:    r.FileFormat,
			Ignore:        r.Ignore,
			IgnoreReason:  r.IgnoreReason,
			LicenseKind:   r.LicenseKind,
			SourceURL:     r.SourceUrl,
			Provenance:    r.ProvenanceNote,
		}
	}
	return rules
}

// Summary holds the result counters from a scan.
type Summary struct {
	Total        int
	Matched      int
	Ignored      int
	Unrecognized int
	Ambiguous    int
	Demoted      int
	Failed       int
}

// Scanner walks dataDir and classifies files against rules.
type Scanner struct {
	DataDir string
	Rules   []Rule
	Log     *slog.Logger
}

// fileEntry is a file discovered during the walk.
type fileEntry struct {
	relPath   string
	absPath   string
	sizeBytes int64
}

// matchResult holds the outcome of matching a file against rules.
type matchResult struct {
	matched       bool
	frameworkCode *string
	versionLabel  *string
	docRole       *string
	qualifier     string
	fileFormat    *string
	ignore        bool
	ignoreReason  string
}

// Match finds the first rule (by ordinal, which is the slice order) whose
// pattern matches relPath. Returns the zero matchResult if no rule matches.
func Match(rules []Rule, relPath string) matchResult {
	for _, r := range rules {
		ok, err := filepath.Match(r.Pattern, relPath)
		if err != nil {
			continue // bad pattern — skip
		}
		if ok {
			if r.Ignore {
				return matchResult{
					matched:      true,
					ignore:       true,
					ignoreReason: r.IgnoreReason,
				}
			}
			return matchResult{
				matched:       true,
				frameworkCode: r.FrameworkCode,
				versionLabel:  r.VersionLabel,
				docRole:       r.DocRole,
				qualifier:     r.Qualifier,
				fileFormat:    r.FileFormat,
			}
		}
	}
	return matchResult{}
}

// Scan walks DataDir, matches files, upserts manifest rows, demotes removed
// files, and detects ambiguity among parse-eligible roles.
func (s *Scanner) Scan(ctx context.Context, q dbingest.Querier) (Summary, error) {
	entries, err := s.walkFiles()
	if err != nil {
		return Summary{}, fmt.Errorf("walk %s: %w", s.DataDir, err)
	}

	var summary Summary
	summary.Total = len(entries)

	var seenPaths []string

	// Track parse-eligible role tuples for ambiguity detection.
	// Key: (framework_code, version_label, doc_role, qualifier, file_format).
	type ambKey struct {
		fw, ver, role, qual, fmt string
	}
	type ambEntry struct {
		relPath string
		id      int64
	}
	ambMap := map[ambKey][]ambEntry{}

	for _, e := range entries {
		seenPaths = append(seenPaths, e.relPath)

		hash, hashErr := sha256File(e.absPath)
		if hashErr != nil {
			// Per-file error: record the file with empty hash so it is not
			// falsely demoted, set stage_error, count as failed, continue.
			s.Log.Error("cannot read file", "path", e.relPath, "err", hashErr)
			m := Match(s.Rules, e.relPath)
			params := dbingest.UpsertManifestFileParams{
				RelPath:       e.relPath,
				Sha256:        "",
				SizeBytes:     e.sizeBytes,
				FrameworkCode: m.frameworkCode,
				VersionLabel:  m.versionLabel,
				DocRole:       m.docRole,
				Qualifier:     m.qualifier,
				FileFormat:    m.fileFormat,
				Ignored:       m.ignore,
				IgnoreReason:  m.ignoreReason,
			}
			row, upsertErr := q.UpsertManifestFile(ctx, params)
			if upsertErr != nil {
				return summary, fmt.Errorf("upsert %s: %w", e.relPath, upsertErr)
			}
			_ = q.SetStageError(ctx, dbingest.SetStageErrorParams{
				ID:         row.ID,
				StageError: fmt.Sprintf("read error: %s", e.relPath),
			})
			summary.Failed++
			continue
		}

		m := Match(s.Rules, e.relPath)

		params := dbingest.UpsertManifestFileParams{
			RelPath:       e.relPath,
			Sha256:        hash,
			SizeBytes:     e.sizeBytes,
			FrameworkCode: m.frameworkCode,
			VersionLabel:  m.versionLabel,
			DocRole:       m.docRole,
			Qualifier:     m.qualifier,
			FileFormat:    m.fileFormat,
			Ignored:       m.ignore,
			IgnoreReason:  m.ignoreReason,
		}

		row, err := q.UpsertManifestFile(ctx, params)
		if err != nil {
			return summary, fmt.Errorf("upsert %s: %w", e.relPath, err)
		}

		switch {
		case m.ignore:
			summary.Ignored++
			s.Log.Debug("ignored", "path", e.relPath, "reason", m.ignoreReason)
		case !m.matched:
			summary.Unrecognized++
			s.Log.Warn("unrecognized file", "path", e.relPath)
		default:
			summary.Matched++
			// Track parse-eligible roles for ambiguity detection.
			if isParseEligible(deref(m.docRole)) {
				k := ambKey{
					fw:   deref(m.frameworkCode),
					ver:  deref(m.versionLabel),
					role: deref(m.docRole),
					qual: m.qualifier,
					fmt:  deref(m.fileFormat),
				}
				ambMap[k] = append(ambMap[k], ambEntry{relPath: e.relPath, id: row.ID})
			}
		}
	}

	// Detect ambiguity: parse-eligible tuples with >1 file.
	for _, entries := range ambMap {
		if len(entries) <= 1 {
			continue
		}
		summary.Ambiguous += len(entries)
		var paths []string
		for _, e := range entries {
			paths = append(paths, e.relPath)
		}
		errMsg := fmt.Sprintf("ambiguous: multiple files for same document identity: %s", strings.Join(paths, ", "))
		for _, e := range entries {
			if err := q.SetStageError(ctx, dbingest.SetStageErrorParams{
				ID:         e.id,
				StageError: errMsg,
			}); err != nil {
				return summary, fmt.Errorf("set ambiguity error for %s: %w", e.relPath, err)
			}
		}
		s.Log.Error("ambiguous files", "paths", paths)
	}

	// Demote removed files.
	if len(seenPaths) > 0 {
		demoted, err := q.DemoteMissingManifestFiles(ctx, seenPaths)
		if err != nil {
			return summary, fmt.Errorf("demote missing: %w", err)
		}
		summary.Demoted = int(demoted)
		if demoted > 0 {
			s.Log.Info("demoted missing files", "count", demoted)
		}
	}

	return summary, nil
}

// walkFiles walks DataDir deterministically (sorted), skipping .git.
// File symlinks are followed for hashing (WalkDir follows symlinks to files);
// broken symlinks surface as per-file errors in the scan loop (hash failure).
func (s *Scanner) walkFiles() ([]fileEntry, error) {
	var entries []fileEntry
	err := filepath.WalkDir(s.DataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Per-file walk error (e.g. permission denied on a directory):
			// skip this entry, don't abort the walk.
			s.Log.Warn("walk error", "path", p, "err", err)
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // skip .git file (submodule gitdir pointer)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.DataDir, p)
		if err != nil {
			s.Log.Warn("rel path error", "path", p, "err", err)
			return nil
		}
		// Normalize to forward slashes for cross-platform consistency.
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			// Stat failure (e.g. broken symlink): record with size 0; the
			// scan loop will handle the hash failure gracefully.
			entries = append(entries, fileEntry{
				relPath:   rel,
				absPath:   p,
				sizeBytes: 0,
			})
			return nil
		}
		entries = append(entries, fileEntry{
			relPath:   rel,
			absPath:   p,
			sizeBytes: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Deterministic order: sort by rel_path.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})
	return entries, nil
}

// isParseEligible returns true for doc_roles that produce parsed documents.
func isParseEligible(role string) bool {
	switch role {
	case "main", "amendment", "companion-workbook":
		return true
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
