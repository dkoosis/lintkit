package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/jsonl"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "jsonl":
		if err := runJSONL(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lintkit <command> [options]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  jsonl   Validate JSONL files against a JSON Schema")
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
