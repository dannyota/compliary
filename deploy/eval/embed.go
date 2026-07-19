// Package eval embeds the golden retrieval-eval set so cmd/eval can load it
// without a filesystem path. The CSV is citation-keyed: queries use our own
// words and citation IDs only -- never licensed document text.
package eval

import _ "embed"

//go:embed golden.csv
var GoldenCSV []byte
