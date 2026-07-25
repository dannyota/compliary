// Package lexical encodes text into BM25 sparse vectors for pgvector's sparsevec
// type — compliary's lexical retrieval arm. BM25 weights are baked into the stored
// DOCUMENT vector (IDF, term saturation, length normalization); the QUERY vector is
// just term presence, so a sparse inner product (pgvector <#>) equals the BM25 score.
//
// Term -> dimension uses the hashing trick (FNV-1a mod Dim), so query-time encoding
// needs no persisted vocabulary — only the same deterministic hash. Lowercasing +
// alnum splitting in the tokenizer make queries match case-insensitively.
//
// Citation tokens (AC-2(3), A.5.1, CC6.1, PR.AA-01, EDM01.01, A&A-01, etc.) are kept
// intact during tokenization so exact citation queries hit. The rule: a citation token
// is a contiguous run of [A-Za-z0-9.()-&] that contains at least one letter and one
// digit (or a dot). Everything else splits on non-alnum boundaries.
//
// Adapted from banhmi pkg/rag/lexical — Vietnamese diacritic folding, Thai TCC
// segmentation, and all non-English normalizers are stripped. Tokenization is
// language-neutral: lowercase + alnum split with diacritics preserved, so each
// Vietnamese syllable of a TCVN chunk is its own token. Only the query-side
// stopword list is language-aware (English + Vietnamese function words).
package lexical

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Dim is the fixed sparsevec dimension. The hashing trick maps each term into
// [1, Dim]; at ~10^5 corpus terms a 2^20 space keeps collisions negligible while
// staying far under pgvector's 16k non-zero-elements-per-vector limit (a chunk
// has at most a few hundred distinct terms).
const Dim = 1 << 20

// BM25 saturation (k1) and length-normalization (b) constants — standard defaults.
const (
	k1 = 1.2
	b  = 0.75
)

// isCitationChar reports whether r can appear inside a citation token.
// Citation tokens may contain letters, digits, dots, hyphens, parentheses,
// and ampersands (CSA CCM uses A&A-01, I&S-05, etc.).
func isCitationChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '(' || r == ')' || r == '&'
}

// isCitationToken reports whether s looks like a framework citation token.
// Two patterns qualify:
//  1. Contains at least one letter AND at least one of {digit, dot}.
//     Catches: AC-2, AC-2(3), A.5.1, CC6.1, PR.AA-01, EDM01.01, CLD.12.1.5.
//  2. Contains at least one digit AND at least one dot (pure numeric citations).
//     Catches: 5.1, 8.3.6, 12.3.4, 1.2.1 — common in CIS, ISO, PCI, SWIFT.
//
// Pure words ("information") and bare integers ("123") fall through to the
// normal alnum-split path.
func isCitationToken(s string) bool {
	hasLetter := false
	hasDigit := false
	hasDot := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		case r == '.':
			hasDot = true
		}
	}
	// Pattern 1: letter + (digit or dot).
	if hasLetter && (hasDigit || hasDot) {
		return true
	}
	// Pattern 2: digit + dot (numeric citation like 5.1, 8.3.6).
	if hasDigit && hasDot {
		return true
	}
	return false
}

// Tokenize lowercases text and splits into tokens. Citation-shaped tokens
// (containing letters + digits/dots, with hyphens/parens/dots allowed) are kept
// intact so queries like "AC-2(3)" or "PR.AA-01" produce a single hash. Other
// text is split on non-alphanumeric boundaries.
func Tokenize(s string) []string {
	s = strings.ToLower(s)

	var tokens []string
	i := 0
	runes := []rune(s)
	n := len(runes)

	for i < n {
		// Skip non-citation, non-alnum characters.
		if !isCitationChar(runes[i]) {
			i++
			continue
		}

		// Collect a maximal run of citation characters.
		j := i
		for j < n && isCitationChar(runes[j]) {
			j++
		}
		tok := string(runes[i:j])
		i = j

		if isCitationToken(tok) {
			// Emit the whole run as one citation token, but also strip
			// trailing dots (sentence punctuation) — "AC-2." -> "ac-2".
			tok = strings.TrimRight(tok, ".")
			if tok != "" {
				tokens = append(tokens, tok)
			}
		} else {
			// Not a citation — split on non-alnum boundaries.
			var cur strings.Builder
			for _, r := range tok {
				if unicode.IsLetter(r) || unicode.IsDigit(r) {
					cur.WriteRune(r)
				} else {
					if cur.Len() > 0 {
						tokens = append(tokens, cur.String())
						cur.Reset()
					}
				}
			}
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
			}
		}
	}
	return tokens
}

// termID maps a term to its 1-based sparsevec index via the hashing trick.
func termID(term string) int32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(term))
	return int32(h.Sum32()%uint32(Dim)) + 1
}

// Encoder holds the trained BM25 statistics needed to build document vectors:
// per-term IDF and the corpus average document length. Build it with Train.
type Encoder struct {
	idf   map[string]float64
	avgdl float64
}

// Train computes IDF (BM25 form) and average document length over the corpus.
// texts is one entry per document (chunk content + any prefix).
func Train(texts []string) *Encoder {
	n := len(texts)
	df := make(map[string]int)
	total := 0
	for _, t := range texts {
		toks := Tokenize(t)
		total += len(toks)
		seen := make(map[string]struct{}, len(toks))
		for _, w := range toks {
			if _, ok := seen[w]; ok {
				continue
			}
			seen[w] = struct{}{}
			df[w]++
		}
	}
	idf := make(map[string]float64, len(df))
	for w, d := range df {
		// BM25 IDF (always-positive variant): ln(1 + (N - df + 0.5)/(df + 0.5)).
		idf[w] = math.Log(1 + (float64(n)-float64(d)+0.5)/(float64(d)+0.5))
	}
	avgdl := 1.0
	if n > 0 && total > 0 {
		avgdl = float64(total) / float64(n)
	}
	return &Encoder{idf: idf, avgdl: avgdl}
}

// DocVector returns the BM25 document sparse vector for text as a pgvector
// sparsevec literal. Terms not seen during Train are skipped (IDF unknown).
func (e *Encoder) DocVector(text string) string {
	toks := Tokenize(text)
	dl := float64(len(toks))
	tf := make(map[string]int, len(toks))
	for _, w := range toks {
		tf[w]++
	}
	weights := make(map[int32]float64, len(tf))
	for w, f := range tf {
		idf, ok := e.idf[w]
		if !ok || idf <= 0 {
			continue
		}
		num := float64(f) * (k1 + 1)
		den := float64(f) + k1*(1-b+b*dl/e.avgdl)
		weights[termID(w)] += idf * num / den
	}
	return sparseLiteral(weights)
}

// queryStopwords are high-frequency terms that appear in nearly every golden-set
// question AND every corpus chunk, contributing no discriminative BM25 signal.
// Filtered at query time only — document vectors stay unchanged so no re-index
// is needed. The list is deliberately short and domain-specific.
//
// Vietnamese entries (TCVN frameworks) are single syllables: the tokenizer
// splits on non-alphanumeric boundaries, so a multi-syllable Vietnamese word is
// several tokens and an entry here removes exactly one syllable. Only grammatical function
// syllables qualify — never a syllable that heads or completes a domain
// compound. Deliberately kept OUT for that reason: có (có thể), không (không
// dây = wireless), phải (the normative "shall"), theo (theo dõi = monitor),
// đối (đối tượng), bằng (cân bằng tải), gồm/bao (bao gồm), một (một lần =
// one-time), vào/ra (ra vào = entry-exit), từ (từ xa = remote, từ chối =
// deny), sao (sao lưu = backup), tại (tồn tại), ai (collides with "AI").
//
// Note the cross-language collision on "an": the English article and the
// Vietnamese syllable of "an toàn"/"an ninh" share one token. It stays
// filtered — the corpus is majority English, so the token is the least
// discriminative one available and would drag English chunks into Vietnamese
// results; the Vietnamese-only second syllable (toàn/ninh) carries the signal.
var queryStopwords = map[string]bool{
	// English.
	"what": true, "how": true, "which": true, "does": true,
	"the": true, "for": true, "and": true, "are": true,
	"is": true, "of": true, "that": true, "a": true,
	"to": true, "in": true, "an": true,
	"control": true, "requirement": true, "cover": true, "address": true,

	// Vietnamese — conjunctions.
	"và": true, "hoặc": true, "nếu": true,
	// Vietnamese — determiners and demonstratives.
	"các": true, "những": true, "này": true, "đó": true,
	// Vietnamese — prepositions and subordinators.
	"của": true, "trong": true, "trên": true, "với": true, "cho": true,
	"về": true, "để": true, "khi": true,
	// Vietnamese — copula and passive marker.
	"là": true, "được": true,
	// Vietnamese — interrogatives (query-side only; absent from the corpus,
	// so their high IDF would otherwise spike unrelated chunks).
	"nào": true, "gì": true,
}

// QueryVector returns the query sparse vector — term presence (1.0) per token —
// as a pgvector sparsevec literal. Stateless: it needs only the shared hash, so
// query-time encoding requires no trained Encoder or persisted vocabulary. The
// inner product with a document vector then equals that document's BM25 score.
// High-frequency stopwords are filtered at query time to improve precision.
func QueryVector(text string) string {
	weights := make(map[int32]float64)
	for _, w := range Tokenize(text) {
		if !queryStopwords[w] {
			weights[termID(w)] = 1.0
		}
	}
	return sparseLiteral(weights)
}

// sparseLiteral renders a pgvector sparsevec literal "{i1:v1,i2:v2,...}/Dim" with
// indices in ascending order (required by pgvector). An empty map renders "{}/Dim".
func sparseLiteral(weights map[int32]float64) string {
	ids := make([]int, 0, len(weights))
	for id := range weights {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, id := range ids {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%d:%g", id, weights[int32(id)])
	}
	fmt.Fprintf(&sb, "}/%d", Dim)
	return sb.String()
}
