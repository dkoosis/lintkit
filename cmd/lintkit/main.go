package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/nuglint"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "nuglint":
		runNuglint(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s nuglint <path> [<path>...]\n", os.Args[0])
}

func runNuglint(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "nuglint requires at least one path")
		os.Exit(1)
	}

	results, err := nuglint.Run(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log := sarif.NewLog()
	log.Runs = append(log.Runs, sarif.Run{
		Tool:    sarif.Tool{Driver: sarif.Driver{Name: "lintkit-nuglint"}},
		Results: results,
	})

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write SARIF: %v\n", err)
		os.Exit(1)
	}
}
