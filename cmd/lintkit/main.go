package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/sarif"
	"github.com/dkoosis/lintkit/pkg/stale"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "stale":
		if err := runStale(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s stale --rules <rules.yml> [PATH...]\n", os.Args[0])
}

func runStale(args []string) error {
	fs := flag.NewFlagSet("stale", flag.ContinueOnError)
	rulesFile := fs.String("rules", "", "Path to the staleness rules file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *rulesFile == "" {
		return fmt.Errorf("--rules is required")
	}

	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	cfg, err := stale.LoadConfig(*rulesFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-stale"}},
	}

	for _, root := range paths {
		results, err := stale.Evaluate(root, cfg)
		if err != nil {
			return err
		}
		run.Results = append(run.Results, results...)
	}

	log.Runs = append(log.Runs, run)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
