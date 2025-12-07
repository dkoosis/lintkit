package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/dbsanity"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "dbsanity":
		if err := runDbSanity(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: lintkit dbsanity --baseline counts.json [--threshold PCT] DB...\n")
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
