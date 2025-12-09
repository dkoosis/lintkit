// Command filesize analyzes Go file sizes and outputs SARIF.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Thresholds for file size warnings
const (
	ThresholdWarning = 500  // LOC for warning
	ThresholdError   = 750  // LOC for error
	ThresholdCrit    = 1000 // LOC for critical
)

type fileInfo struct {
	path  string
	lines int
}

func main() {
	dir := flag.String("dir", ".", "directory to analyze")
	topN := flag.Int("top", 10, "number of largest files to report")
	flag.Parse()

	files, err := analyzeDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	log := buildSARIF(files, *topN)
	enc := sarif.NewEncoder(os.Stdout)
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

		// Skip hidden directories and vendor
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
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

func buildSARIF(files []fileInfo, topN int) *sarif.Log {
	log := sarif.NewLog()

	run := sarif.Run{
		Tool: sarif.Tool{
			Driver: sarif.Driver{
				Name:    "filesize",
				Version: "0.1.0",
			},
		},
		Results: []sarif.Result{},
	}

	// Report top N largest files
	count := topN
	if count > len(files) {
		count = len(files)
	}

	for i := 0; i < count; i++ {
		f := files[i]
		level, ruleID := classifyFile(f.lines)

		result := sarif.Result{
			RuleID: ruleID,
			Level:  level,
			Message: sarif.Message{
				Text: fmt.Sprintf("%s: %d lines", f.path, f.lines),
			},
			Locations: []sarif.Location{
				{
					PhysicalLocation: sarif.PhysicalLocation{
						ArtifactLocation: sarif.ArtifactLocation{
							URI: f.path,
						},
					},
				},
			},
		}
		run.Results = append(run.Results, result)
	}

	// Add summary stats
	stats := computeStats(files)
	run.Results = append(run.Results, sarif.Result{
		RuleID: "filesize/summary",
		Level:  "note",
		Message: sarif.Message{
			Text: fmt.Sprintf("total=%d over500=%d over750=%d over1000=%d",
				stats.total, stats.over500, stats.over750, stats.over1000),
		},
	})

	log.Runs = append(log.Runs, run)
	return log
}

func classifyFile(lines int) (level, ruleID string) {
	switch {
	case lines >= ThresholdCrit:
		return "error", "filesize/critical"
	case lines >= ThresholdError:
		return "error", "filesize/large"
	case lines >= ThresholdWarning:
		return "warning", "filesize/medium"
	default:
		return "note", "filesize/ok"
	}
}

type stats struct {
	total    int
	over500  int
	over750  int
	over1000 int
}

func computeStats(files []fileInfo) stats {
	var s stats
	s.total = len(files)
	for _, f := range files {
		if f.lines >= 500 {
			s.over500++
		}
		if f.lines >= 750 {
			s.over750++
		}
		if f.lines >= 1000 {
			s.over1000++
		}
	}
	return s
}
