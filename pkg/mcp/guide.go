package mcp

// Guide returns the static evidence-playbook text. It tells a connecting agent
// what compliary covers, how to use the five tools, and the evidence contract.
func (c *Core) Guide() GuideOutput {
	return guidePayload
}

// GuideOutput is the guide tool's structured result.
type GuideOutput struct {
	Purpose          string      `json:"purpose"`
	RecommendedFlow  []string    `json:"recommended_flow"`
	Tools            []GuideTool `json:"tools"`
	EvidenceContract []string    `json:"evidence_contract"`
}

// GuideTool is one tool entry in the guide.
type GuideTool struct {
	Name string `json:"name"`
	Use  string `json:"use"`
}

var guidePayload = GuideOutput{
	Purpose: "compliary exposes InfoSec & cybersecurity control frameworks (ISO 27001, SOC 2 TSC, PCI DSS, NIST CSF, NIST SP 800-53, CIS Controls, ISO 27002/27017/27018, CSA CCM, COBIT, and more) as citable database evidence for a user-owned agent/model. You decide the answer; compliary never synthesizes one. Each control carries its exact citation, version lineage, cross-framework mapping edges, provenance, and explicit gaps. Query in English (the frameworks' publication language).",

	RecommendedFlow: []string{
		"Call corpus_status first to see which frameworks are indexed, their versions, and known gaps.",
		"Call search with a compliance question or control citation. Use the framework filter for ~82% recall (vs ~72% unfiltered) when you know the target framework. Citation-shaped queries (AC-2, A.5.1, CC6.1) get a direct DB lookup pinned at score 1.0.",
		"Call document with a citation to read one control: its body (verbatim text past auth), amended_by patches (e.g. ISO 27001 Amd 1:2024 climate edits), mapping edges (both directions, resolved + unresolved), version lineage, and parent/children context. Use the include array ([\"chunks\",\"mappings\",\"lineage\",\"children\"]) to select sections.",
		"Token economy: document with include=[\"chunks\"] is the cheapest way to read one control's text; add mappings/lineage/children only when you need them (omitting include returns all four). For discovery, lower search top_k rather than paging document chunks.",
		"Call quality_gaps to see unresolved mapping edges, deferred documents, body-quality caveats, and abstain/eval floors.",
		"Answer only from returned evidence; surface gaps and unresolved edges instead of guessing.",
	},

	Tools: []GuideTool{
		{Name: "corpus_status", Use: "Live per-framework/version counts (documents, controls by kind, withdrawn, chunks, embeddings, mapping edges resolved/unresolved), completeness, and last-stage info. mapping_edges counts edges FROM the version; inbound_edges counts edges resolved INTO it — read both before calling a framework unmapped."},
		{Name: "search", Use: "Hybrid retrieval (dense + BM25, RRF-fused). Accepts framework and version_label filters, include_withdrawn flag, top_k, mode. Citation-shaped queries are pinned at score 1.0. Score-floor abstention returns an explicit gap notice when the top score is too low. Returns structured hits with citations, scores, and version status."},
		{Name: "document", Use: "Citation lookup: control body (verbatim past auth), amended_by amendment patches, mapping edges (both directions, resolved + unresolved with honest labels; sources: publisher-catalog, nist-olir, cis-v8.1-mappings, iso-structural), version lineage (version_relation + framework_version currency), parent/children context. Default version = current; explicit version pin supported; include array selects sections."},
		{Name: "quality_gaps", Use: "Unresolved mapping edges by target, deferred docs (amendments, CAIQ), unrecognized manifest rows, body-quality caveats (PCI guidance interleave), abstain/eval floors."},
		{Name: "guide", Use: "This playbook: scope, citation forms, query tips, evidence contract."},
	},

	EvidenceContract: []string{
		"hits are ranked control evidence with RRF fusion scores; citation-pinned hits lead at score 1.0.",
		"compliary returns structured data, never generated prose or summaries.",
		"search always returns hits even when abstain is true; abstain marks a gap, not that hits are wrong. Read gaps[].kind to learn why.",
		"gap kinds: no_evidence (nothing matched), low_confidence (best vector similarity below the abstention floor), unknown_framework (the framework filter names a code not in the corpus; the gap lists valid codes), version_not_found (the pinned version is not in the corpus; the gap lists available versions), ambiguous_citation (a bare citation like 5.1 exists in several frameworks; the gap lists them — pass framework_code to pin), found_elsewhere (the citation is absent from the pinned framework but exists in the listed ones), no_chunks (chunks were requested but the control has none at this offset).",
		"honesty limits: recall is ~82% with a framework filter, ~72% open-corpus — roughly 1 in 5 unfiltered in-scope queries misses the best control, so corroborate important negatives with document lookups. Abstention catches clearly out-of-scope queries only; compliance-adjacent topics (export control, medical devices) sit too close to InfoSec text to separate.",
		"hits and controls carry source_url — the official publisher page for verification; it is provenance, not a text mirror.",
		"document returns the full control body only under full projection (authenticated/local callers). Reduced projection (unauthenticated HTTP) strips body/title_original/chunk content AND amended_by instruction bodies, keeping citations, paraphrased titles, scores, mapping edges, and amendment metadata.",
		"mapping edges carry resolved (to_control_id set) and unresolved (honest label) statuses. Unresolved edges name the target framework/citation but link to no parsed control — the target document may not be ingested yet.",
		"version lineage: version_relation rows (supersedes/amends) plus framework_version.is_current. Superseded versions are served flagged, never as current.",
		"compliary never answers; it returns evidence and the connecting model decides.",
	},
}
