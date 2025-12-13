package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dkoosis/lintkit/pkg/dbsanity"
	"github.com/dkoosis/lintkit/pkg/dbschema"
	"github.com/dkoosis/lintkit/pkg/docsprawl"
	"github.com/dkoosis/lintkit/pkg/filesize"
	"github.com/dkoosis/lintkit/pkg/jsonl"
	"github.com/dkoosis/lintkit/pkg/nobackups"
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
	case "filesize":
		if err := runFilesize(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "nobackups":
		if err := runNoBackups(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "jsonl":
		if err := runJSONL(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "dbschema":
		if err := runDbSchema(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
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
	fmt.Fprintln(flag.CommandLine.Output(), "  filesize     Check file sizes against budget rules")
	fmt.Fprintln(flag.CommandLine.Output(), "  nobackups    Detect backup/temporary files")
	fmt.Fprintln(flag.CommandLine.Output(), "  jsonl        Validate JSONL files against JSON Schema")
	fmt.Fprintln(flag.CommandLine.Output(), "  dbschema     Compare SQLite schemas against expected DDL")
}

func runDbSanity(args []string) error {
	fs := flag.NewFlagSet("dbsanity", flag.ExitOnError)
	baselinePath := fs.String("baseline", "", "Path to baseline JSON with expected table counts")
	threshold := fs.Float64("threshold", 20, "Percentage threshold for drift detection")
	configPath := fs.String("config", "", "Path to YAML config for data checks")
	historyPath := fs.String("history", "", "Path to history JSON file for WoW tracking")
	updateHistory := fs.Bool("update", false, "Update history file with current results")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: lintkit dbsanity [--baseline counts.json | --config checks.yaml] DB...\n")
		fmt.Fprintf(fs.Output(), "\nModes:\n")
		fmt.Fprintf(fs.Output(), "  Legacy:  --baseline counts.json [--threshold PCT]\n")
		fmt.Fprintf(fs.Output(), "  Checks:  --config checks.yaml [--history history.json] [--update]\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	dbPaths := fs.Args()
	if len(dbPaths) == 0 {
		fs.Usage()
		return fmt.Errorf("at least one database path is required")
	}

	// Config-based mode
	if *configPath != "" {
		return runDbSanityChecks(dbPaths, *configPath, *historyPath, *updateHistory)
	}

	// Legacy baseline mode
	if *baselinePath == "" {
		fs.Usage()
		return fmt.Errorf("either --baseline or --config is required")
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

func runDbSanityChecks(dbPaths []string, configPath, historyPath string, updateHistory bool) error {
	cfg, err := dbsanity.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var history dbsanity.History
	if historyPath != "" {
		history, err = dbsanity.LoadHistory(historyPath)
		if err != nil {
			return fmt.Errorf("load history: %w", err)
		}
	}

	now := time.Now()
	currentWeek := dbsanity.ISOWeek(now)

	var allResults []sarif.Result
	allCheckResults := make(map[string]dbsanity.CheckResult)

	for _, dbPath := range dbPaths {
		checkResults, err := dbsanity.RunChecks(context.Background(), dbPath, cfg)
		if err != nil {
			return fmt.Errorf("checks on %s: %w", dbPath, err)
		}

		for k, v := range checkResults {
			allCheckResults[k] = v
		}

		results := dbsanity.CompareWithHistory(dbPath, checkResults, &history, currentWeek)
		allResults = append(allResults, results...)
	}

	// Update history if requested
	if updateHistory && historyPath != "" {
		snapshot := dbsanity.Snapshot{
			Timestamp: now,
			Week:      currentWeek,
			Results:   allCheckResults,
		}
		history.AddSnapshot(snapshot)
		if err := dbsanity.SaveHistory(historyPath, history); err != nil {
			return fmt.Errorf("save history: %w", err)
		}
	}

	log := dbsanity.BuildLog(allResults)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
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

func runFilesize(args []string) error {
	fs := flag.NewFlagSet("filesize", flag.ContinueOnError)
	rulesPath := fs.String("rules", "", "Path to YAML rules file")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *rulesPath == "" {
		return fmt.Errorf("--rules is required")
	}

	analyzerRules, err := filesize.LoadRules(*rulesPath)
	if err != nil {
		return err
	}

	analyzer := filesize.NewAnalyzer(analyzerRules)
	log, err := analyzer.Analyze(fs.Args())
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func runNoBackups(paths []string) error {
	if len(paths) == 0 {
		paths = []string{"."}
	}

	log, err := nobackups.Scan(paths)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func runJSONL(args []string) error {
	fs := flag.NewFlagSet("jsonl", flag.ContinueOnError)
	schemaPath := fs.String("schema", "", "path to JSON Schema file")
	fs.SetOutput(os.Stderr)

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *schemaPath == "" {
		return errors.New("--schema is required")
	}

	files := fs.Args()
	if len(files) == 0 {
		return errors.New("at least one JSONL path is required")
	}

	validator, err := jsonl.NewValidator(*schemaPath)
	if err != nil {
		return err
	}

	log := sarif.NewLog()
	run := sarif.Run{Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-jsonl"}}}

	for _, path := range files {
		results, err := jsonl.ValidateFile(path, validator)
		if err != nil {
			return err
		}
		run.Results = append(run.Results, results...)
	}

	log.Runs = append(log.Runs, run)

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(log); err != nil {
		return err
	}

	if len(run.Results) > 0 {
		return errors.New("validation errors detected")
	}

	return nil
}

func runDbSchema(args []string) error {
	fs := flag.NewFlagSet("dbschema", flag.ExitOnError)
	expectedPath := fs.String("expected", "", "Path to expected schema DDL file")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: lintkit dbschema --expected schema.sql DB...\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *expectedPath == "" {
		return fmt.Errorf("--expected is required")
	}

	dbPaths := fs.Args()
	if len(dbPaths) == 0 {
		return fmt.Errorf("at least one database path is required")
	}

	expectedFile, err := os.Open(*expectedPath)
	if err != nil {
		return fmt.Errorf("open expected schema: %w", err)
	}
	defer expectedFile.Close()

	expected, err := dbschema.ParseExpectedSchema(expectedFile)
	if err != nil {
		return err
	}

	log := sarif.NewLog()
	run := sarif.Run{Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-dbschema"}}}

	ctx := context.Background()
	for _, dbPath := range dbPaths {
		actual, err := dbschema.LoadActualSchema(ctx, dbPath)
		if err != nil {
			return err
		}

		findings := dbschema.CompareSchemas(expected, actual)
		run.Results = append(run.Results, dbschema.ToSARIF(dbPath, findings)...)
	}

	log.Runs = append(log.Runs, run)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
