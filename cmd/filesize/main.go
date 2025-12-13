// Command filesize analyzes Go file sizes and outputs SARIF.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Thresholds for file size buckets
const (
	ThresholdYellow = 500  // LOC for yellow (warning)
	ThresholdRed    = 1000 // LOC for red (error)
)

type fileInfo struct {
	path  string
	lines int
}

func main() {
	dir := flag.String("dir", ".", "directory to analyze")
	flag.Parse()

	files, err := analyzeDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	log := buildSARIF(files)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding SARIF: %v\n", err)
		os.Exit(1)
	}
}

func analyzeDir(root string) ([]fileInfo, error) {
	var files []fileInfo

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and vendor (but not the root ".")
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only analyze .go files, skip tests
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		lines, err := countLines(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		files = append(files, fileInfo{path: path, lines: lines})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by line count descending
	sort.Slice(files, func(i, j int) bool {
		return files[i].lines > files[j].lines
	})

	return files, nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path) //nolint:gosec // path from walkdir
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func buildSARIF(files []fileInfo) *sarif.Log {
	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-filesize"}},
	}

	for _, f := range files {
		var level string
		var ruleID string

		switch {
		case f.lines >= ThresholdRed:
			level = "error"
			ruleID = "filesize-red"
		case f.lines >= ThresholdYellow:
			level = "warning"
			ruleID = "filesize-yellow"
		default:
			continue // Green files don't get reported
		}

		run.Results = append(run.Results, sarif.Result{
			RuleID: ruleID,
			Level:  level,
			Message: sarif.Message{
				Text: fmt.Sprintf("%s has %d lines", filepath.ToSlash(f.path), f.lines),
			},
			Locations: []sarif.Location{{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: filepath.ToSlash(f.path)},
				},
			}},
		})
	}

	log.Runs = append(log.Runs, run)
	return log
}
