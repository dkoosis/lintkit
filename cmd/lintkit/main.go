package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/dbschema"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "dbschema":
		if err := runDbSchema(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [options]\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  dbschema    Compare SQLite schemas against expected DDL")
}

func runDbSchema(args []string) error {
	fs := flag.NewFlagSet("dbschema", flag.ExitOnError)
	expectedPath := fs.String("expected", "", "Path to expected schema DDL file")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s dbschema --expected schema.sql DB...\n", os.Args[0])
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
