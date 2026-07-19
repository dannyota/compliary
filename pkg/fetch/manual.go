package fetch

import (
	"os"
	"path/filepath"
)

// Manual drop-in sources: sign-in, purchase, or membership gated — never
// automated. The operator obtains each document and saves it under dataDir.
var manualSources = []struct {
	name string
	url  string
	dir  string
}{
	{"ISO/IEC 27001:2022 (+Amd 1:2024)", "https://www.iso.org/standard/27001", "iso"},
	{"ISO/IEC 27002:2022", "https://www.iso.org/standard/75652.html", "iso"},
	{"ISO/IEC 27017:2015", "https://www.iso.org/standard/43757.html", "iso"},
	{"ISO/IEC 27018:2019", "https://www.iso.org/standard/76559.html", "iso"},
	{"ISO/IEC 27701:2025", "https://www.iso.org/standard/27701", "iso"},
	{"ISO 22301:2019", "https://www.iso.org/standard/75106.html", "iso"},
	{"ISO/IEC 42001:2023", "https://www.iso.org/standard/42001", "iso"},
	{"AICPA 2017 TSC (2022 Points of Focus)", "https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022", "aicpa"},
	{"SWIFT CSCF v2026 (Knowledge Centre, members only)", "https://www2.swift.com/knowledgecentre/", "swift"},
	{"COBIT 2019 Framework (ISACA)", "https://www.isaca.org/resources/cobit", "isaca"},
	{"CSA CCM v4", "https://cloudsecurityalliance.org/research/cloud-controls-matrix", "csa"},
}

// Manual reports drop-in instructions for every gated source whose data
// directory is still empty.
func Manual(dataDir string, report func(string)) {
	seen := map[string]bool{}
	pending := false
	for _, s := range manualSources {
		dir := filepath.Join(dataDir, s.dir)
		if !seen[s.dir] {
			seen[s.dir] = true
			_ = os.MkdirAll(dir, 0o755)
		}
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && s.dir != "iso" {
			continue
		}
		if !pending {
			report("manual drop-in required (sign-in/purchase/membership — never automated):")
			pending = true
		}
		report("  " + s.name)
		report("    get: " + s.url)
		report("    save into: " + dir + "/")
	}
	if !pending {
		report("manual sources: all data directories have files")
	}
}
