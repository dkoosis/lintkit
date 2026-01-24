// Command filesize analyzes Go file sizes and outputs SARIF, check, or human-readable format.
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

	"github.com/dkoosis/lintkit/pkg/check"
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

// metrics holds aggregate file size metrics during analysis.
type metrics struct {
	total  int
	green  int
	yellow int
	red    int
}

func main() {
	dir := flag.String("dir", ".", "directory to analyze")
	format := flag.String("format", "human", "output format: sarif, check, human")
	top := flag.Int("top", 10, "limit output to top N files (0=all)")
	flag.Parse()

	files, err := analyzeDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "sarif":
		outputSARIF(files, *top)
	case "check":
		outputCheck(files, *top)
	default:
		outputHuman(files, *top)
	}
}

func outputSARIF(files []fileInfo, top int) {
	if top > 0 && len(files) > top {
		files = files[:top]
	}
	log := buildSARIF(files)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding SARIF: %v\n", err)
		os.Exit(1)
	}
}

func buildReport(files []fileInfo, top int) *check.Report {
	// Count by category (from all files)
	m := computeMetrics(files)

	// Build report
	report := check.NewReport("filesize")

	// Add metrics
	report.Metrics = []check.Metric{
		{Name: "red_files", Value: float64(m.red), Threshold: 0, Op: check.OpLTE},
		{Name: "yellow_files", Value: float64(m.yellow)},
		{Name: "green_files", Value: float64(m.green)},
		{Name: "total_files", Value: float64(m.total)},
	}

	// Apply top limit for items
	displayFiles := files
	if top > 0 && len(displayFiles) > top {
		displayFiles = displayFiles[:top]
	}

	// Add items (only yellow and red files)
	for _, f := range displayFiles {
		if f.lines < ThresholdYellow {
			continue
		}

		sev := check.SeverityWarn
		if f.lines >= ThresholdRed {
			sev = check.SeverityError
		}

		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    filepath.Base(f.path),
			Value:    fmt.Sprintf("%d LOC", f.lines),
			Path:     f.path,
		})
	}

	// Set summary
	if m.red == 0 && m.yellow == 0 {
		report.Summary = "All files within size limits"
	} else {
		var parts []string
		if m.red > 0 {
			parts = append(parts, fmt.Sprintf("%d critical (>%d LOC)", m.red, ThresholdRed))
		}
		if m.yellow > 0 {
			parts = append(parts, fmt.Sprintf("%d warning (>%d LOC)", m.yellow, ThresholdYellow))
		}
		report.Summary = strings.Join(parts, ", ")
	}

	return report
}

func computeMetrics(files []fileInfo) metrics {
	var m metrics
	m.total = len(files)
	for _, f := range files {
		switch {
		case f.lines >= ThresholdRed:
			m.red++
		case f.lines >= ThresholdYellow:
			m.yellow++
		default:
			m.green++
		}
	}
	return m
}

func outputCheck(files []fileInfo, top int) {
	report := buildReport(files, top)
	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func outputHuman(files []fileInfo, top int) {
	report := buildReport(files, top)
	cfg := check.DefaultHumanConfig()
	cfg.Purpose = "Identify oversized Go files that may need splitting"
	report.WriteHuman(os.Stdout, cfg)
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

		// Only analyze .go files (skip test files)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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

	// Sort source files by line count descending
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
	defer func() { _ = f.Close() }()

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
