// Package seed embeds the shipped config defaults (deploy/seed/*.csv) so
// cmd/seed needs no external files at runtime. Seeds carry registry metadata
// and our own paraphrased names only — never licensed document text.
package seed

import "embed"

// FS holds every embedded seed CSV.
//
//go:embed *.csv
var FS embed.FS
