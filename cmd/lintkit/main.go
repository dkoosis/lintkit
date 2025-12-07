package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/wikifmt"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s wikifmt ROOT...\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()
	args := flag.Args()
	if len(args) < 1 || args[0] != "wikifmt" {
		flag.Usage()
		os.Exit(2)
	}

	roots := args[1:]
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "lintkit wikifmt requires at least one ROOT directory")
		os.Exit(2)
	}

	log, err := wikifmt.Run(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode SARIF: %v\n", err)
		os.Exit(1)
	}
}
