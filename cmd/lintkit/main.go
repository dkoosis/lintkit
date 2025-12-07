package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dkoosis/lintkit/pkg/nobackups"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "nobackups":
		if err := runNoBackups(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func runNoBackups(paths []string) error {
	log, err := nobackups.Scan(paths)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lintkit nobackups [PATH...]")
}
