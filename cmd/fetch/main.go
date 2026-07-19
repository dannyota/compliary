// Command fetch is the one-shot corpus downloader. It loads the operator
// identity from the gitignored env file (prompting once for anything
// missing), downloads every automatable source into the data directory, and
// prints drop-in instructions for sign-in/purchase/membership-gated sources.
package main

import (
	"flag"
	"fmt"
	"os"

	"danny.vn/compliary/pkg/fetch"
	"danny.vn/compliary/pkg/operator"
)

func main() {
	envPath := flag.String("env", ".env", "operator identity env file")
	dataDir := flag.String("data", "data", "download target directory")
	flag.Parse()

	report := func(msg string) { fmt.Println(msg) }

	id, err := operator.Load(*envPath, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "identity:", err)
		os.Exit(1)
	}

	client, err := fetch.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	failed := false
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		failed = true
	}

	report("== NIST (public domain, direct) ==")
	if err := fetch.NIST(client, *dataDir, report); err != nil {
		fail(err)
	}

	report("== PCI SSC (license click-through as operator) ==")
	if err := fetch.PCISSC(client, *dataDir, id, report); err != nil {
		fail(err)
	}

	report("== CIS (public download page) ==")
	if err := fetch.CIS(client, *dataDir, report); err != nil {
		fail(err)
	}

	report("== Gated sources ==")
	fetch.Manual(*dataDir, report)

	if failed {
		os.Exit(1)
	}
}
