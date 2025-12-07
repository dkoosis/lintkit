package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/docsprawl"
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
}
