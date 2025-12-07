package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dkoosis/lintkit/pkg/filesize"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("lintkit-filesize", flag.ContinueOnError)
	fs.SetOutput(out)
	rulesPath := fs.String("rules", "", "Path to YAML rules file")

	if err := fs.Parse(args); err != nil {
		return err
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

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}
