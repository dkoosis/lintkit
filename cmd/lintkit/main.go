package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/dbsanity"
	"github.com/dkoosis/lintkit/pkg/docsprawl"
	"github.com/dkoosis/lintkit/pkg/nuglint"
	"github.com/dkoosis/lintkit/pkg/sarif"
	"github.com/dkoosis/lintkit/pkg/stale"
	"github.com/dkoosis/lintkit/pkg/wikifmt"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	subcommand := os.Args[1]
	switch subcommand {
	case "docsprawl":
		fs := docsprawl.Command()
		if err := docsprawl.RunCLI(fs, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "dbsanity":
		if err := runDbSanity(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "wikifmt":
		if err := runWikifmt(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "stale":
		if err := runStale(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "nuglint":
		runNuglint(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", subcommand)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(flag.CommandLine.Output(), "Usage: lintkit <command> [options]")
	fmt.Fprintln(flag.CommandLine.Output(), "Commands:")
	fmt.Fprintln(flag.CommandLine.Output(), "  docsprawl    Analyze markdown sprawl and emit SARIF")
	fmt.Fprintln(flag.CommandLine.Output(), "  dbsanity     Check SQLite row counts against baseline")
	fmt.Fprintln(flag.CommandLine.Output(), "  wikifmt      Check wiki-style markdown files")
	fmt.Fprintln(flag.CommandLine.Output(), "  stale        Detect stale artifacts based on mtime rules")
	fmt.Fprintln(flag.CommandLine.Output(), "  nuglint      Lint ORCA knowledge nugget JSONL files")
}

func runDbSanity(args []string) error {
	fs := flag.NewFlagSet("dbsanity", flag.ExitOnError)
	baselinePath := fs.String("baseline", "", "Path to baseline JSON with expected table counts")
	threshold := fs.Float64("threshold", 20, "Percentage threshold for drift detection")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: lintkit dbsanity --baseline counts.json [--threshold PCT] DB...\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *baselinePath == "" {
		fs.Usage()
		return fmt.Errorf("baseline path is required")
	}

	dbPaths := fs.Args()
	if len(dbPaths) == 0 {
		fs.Usage()
		return fmt.Errorf("at least one database path is required")
	}

	baseline, err := dbsanity.LoadBaseline(*baselinePath)
	if err != nil {
		return fmt.Errorf("failed to load baseline: %w", err)
	}

	var totalFindings []sarif.Result
	for _, dbPath := range dbPaths {
		results, err := dbsanity.CheckDatabase(context.Background(), dbPath, baseline, *threshold)
		if err != nil {
			return fmt.Errorf("checking %s: %w", dbPath, err)
		}
		totalFindings = append(totalFindings, results...)
	}

	log := dbsanity.BuildLog(totalFindings)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		return err
	}

	if len(totalFindings) > 0 {
		return fmt.Errorf("dbsanity detected drift in %d table(s)", len(totalFindings))
	}

	return nil
}

func runWikifmt(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "lintkit wikifmt requires at least one ROOT directory")
		return fmt.Errorf("no ROOT directories provided")
	}

	log, err := wikifmt.Run(args)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		return fmt.Errorf("failed to encode SARIF: %w", err)
	}

	return nil
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
