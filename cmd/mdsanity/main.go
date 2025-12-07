package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/mdsanity"
)

func main() {
	root := flag.String("root", ".", "repository root to analyze")
	flag.Parse()

	log, err := mdsanity.Run(mdsanity.Config{RepoRoot: *root})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mdsanity: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "mdsanity: failed to encode SARIF: %v\n", err)
		os.Exit(1)
	}
}
