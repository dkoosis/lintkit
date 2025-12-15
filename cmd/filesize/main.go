// Command filesize analyzes Go file sizes and outputs SARIF, text, or dashboard JSON.
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
	"time"

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

// DashboardOutput is the comprehensive output format for dashboard rendering.
type DashboardOutput struct {
	Timestamp time.Time        `json:"timestamp"`
	Metrics   DashboardMetrics `json:"metrics"`
	TopFiles  []DashboardFile  `json:"top_files"`
	History   []HistoryEntry   `json:"history,omitempty"`
}

// DashboardMetrics contains aggregate file size metrics.
type DashboardMetrics struct {
	// Source file counts by size tier
	Total  int `json:"total"`
	Green  int `json:"green"`  // < 500 LOC
	Yellow int `json:"yellow"` // 500-999 LOC
	Red    int `json:"red"`    // >= 1000 LOC

	// Additional file counts
	TestFiles   int `json:"test_files"`   // _test.go files
	MDFiles     int `json:"md_files"`     // .md files
	OrphanMD    int `json:"orphan_md"`    // MD files not linked from/to
}

// DashboardFile represents a single file in the top N list.
type DashboardFile struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	Tier  string `json:"tier"` // "green", "yellow", "red"
}

// HistoryEntry represents a historical snapshot for trend display.
type HistoryEntry struct {
	Week      string `json:"week"` // e.g., "Week -0", "Week -1"
	Total     int    `json:"total"`
	Green     int    `json:"green"`
	Yellow    int    `json:"yellow"`
	Red       int    `json:"red"`
	TestFiles int    `json:"test_files,omitempty"`
	MDFiles   int    `json:"md_files,omitempty"`
	OrphanMD  int    `json:"orphan_md,omitempty"`
}

func main() {
	dir := flag.String("dir", ".", "directory to analyze")
	format := flag.String("format", "sarif", "output format: sarif, text, dashboard")
	top := flag.Int("top", 0, "limit output to top N files (0=all)")
	snapshotFile := flag.String("snapshots", "", "path to snapshots JSONL file for history (dashboard format only)")
	flag.Parse()

	files, err := analyzeDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "text":
		outputText(files, *top)
	case "dashboard":
		outputDashboard(*dir, *top, *snapshotFile)
	default:
		outputSARIF(files, *top)
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

func outputText(files []fileInfo, top int) {
	// Count by category (from all files)
	var red, yellow, green int
	for _, f := range files {
		switch {
		case f.lines >= ThresholdRed:
			red++
		case f.lines >= ThresholdYellow:
			yellow++
		default:
			green++
		}
	}

	// Apply top limit for display
	if top > 0 && len(files) > top {
		files = files[:top]
	}

	// Summary line
	if red == 0 && yellow == 0 {
		fmt.Println("All files within size limits")
		return
	}

	parts := []string{}
	if red > 0 {
		parts = append(parts, fmt.Sprintf("%d critical (>%d)", red, ThresholdRed))
	}
	if yellow > 0 {
		parts = append(parts, fmt.Sprintf("%d warning (>%d)", yellow, ThresholdYellow))
	}
	fmt.Println(strings.Join(parts, " | "))
	fmt.Println()

	// List files
	for _, f := range files {
		if f.lines < ThresholdYellow {
			continue // Skip green files
		}

		icon := "△"
		if f.lines >= ThresholdRed {
			icon = "✗"
		}
		fmt.Printf("  %s %-50s %d lines\n", icon, f.path, f.lines)
	}
}

func outputDashboard(dir string, top int, snapshotFile string) {
	// Get full analysis including MD files, test files, etc.
	analysis, err := analyzeAll(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error analyzing directory: %v\n", err)
		os.Exit(1)
	}

	// Calculate metrics from all source files
	metrics := DashboardMetrics{Total: len(analysis.sourceFiles)}
	for _, f := range analysis.sourceFiles {
		switch {
		case f.lines >= ThresholdRed:
			metrics.Red++
		case f.lines >= ThresholdYellow:
			metrics.Yellow++
		default:
			metrics.Green++
		}
	}

	// Add additional metrics
	metrics.TestFiles = analysis.testCount
	metrics.MDFiles = len(analysis.mdFiles)
	metrics.OrphanMD = analysis.orphanMD

	// Get top files
	topCount := top
	if topCount <= 0 || topCount > len(analysis.sourceFiles) {
		topCount = len(analysis.sourceFiles)
	}
	if topCount > 10 {
		topCount = 10 // Dashboard limit
	}

	topFiles := make([]DashboardFile, topCount)
	for i := 0; i < topCount; i++ {
		f := analysis.sourceFiles[i]
		tier := "green"
		if f.lines >= ThresholdRed {
			tier = "red"
		} else if f.lines >= ThresholdYellow {
			tier = "yellow"
		}
		topFiles[i] = DashboardFile{
			Path:  filepath.Base(f.path), // Just filename, not full path
			Lines: f.lines,
			Tier:  tier,
		}
	}

	// Load history if snapshot file provided
	var history []HistoryEntry
	if snapshotFile != "" {
		history = loadHistory(snapshotFile)
	}

	output := DashboardOutput{
		Timestamp: time.Now(),
		Metrics:   metrics,
		TopFiles:  topFiles,
		History:   history,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding dashboard output: %v\n", err)
		os.Exit(1)
	}

	// Save current snapshot if file provided
	if snapshotFile != "" {
		saveSnapshot(snapshotFile, metrics)
	}
}

// Snapshot format for JSONL storage
type snapshot struct {
	Ts        time.Time `json:"ts"`
	Total     int       `json:"total"`
	Green     int       `json:"green"`
	Yellow    int       `json:"yellow"`
	Red       int       `json:"red"`
	TestFiles int       `json:"test_files,omitempty"`
	MDFiles   int       `json:"md_files,omitempty"`
	OrphanMD  int       `json:"orphan_md,omitempty"`
}

func loadHistory(path string) []HistoryEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var snapshots []snapshot
	decoder := json.NewDecoder(f)
	for decoder.More() {
		var s snapshot
		if err := decoder.Decode(&s); err != nil {
			continue
		}
		snapshots = append(snapshots, s)
	}

	// Group by week and get the last 8 weeks
	now := time.Now()
	history := make([]HistoryEntry, 8)
	for i := range 8 {
		weekStart := now.AddDate(0, 0, -7*(i+1))
		weekEnd := now.AddDate(0, 0, -7*i)
		history[i] = HistoryEntry{Week: fmt.Sprintf("Week -%d", i)}

		// Find most recent snapshot in this week
		for j := len(snapshots) - 1; j >= 0; j-- {
			s := snapshots[j]
			if s.Ts.After(weekStart) && s.Ts.Before(weekEnd) {
				history[i].Total = s.Total
				history[i].Green = s.Green
				history[i].Yellow = s.Yellow
				history[i].Red = s.Red
				history[i].TestFiles = s.TestFiles
				history[i].MDFiles = s.MDFiles
				history[i].OrphanMD = s.OrphanMD
				break
			}
		}
	}

	return history
}

func saveSnapshot(path string, metrics DashboardMetrics) {
	// Read existing snapshots
	var snapshots []snapshot
	if f, err := os.Open(path); err == nil {
		decoder := json.NewDecoder(f)
		for decoder.More() {
			var s snapshot
			if err := decoder.Decode(&s); err == nil {
				snapshots = append(snapshots, s)
			}
		}
		f.Close()
	}

	// Add current snapshot
	snapshots = append(snapshots, snapshot{
		Ts:        time.Now(),
		Total:     metrics.Total,
		Green:     metrics.Green,
		Yellow:    metrics.Yellow,
		Red:       metrics.Red,
		TestFiles: metrics.TestFiles,
		MDFiles:   metrics.MDFiles,
		OrphanMD:  metrics.OrphanMD,
	})

	// Trim to last 35 days
	cutoff := time.Now().AddDate(0, 0, -35)
	var trimmed []snapshot
	for _, s := range snapshots {
		if s.Ts.After(cutoff) {
			trimmed = append(trimmed, s)
		}
	}

	// Rewrite file
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, s := range trimmed {
		enc.Encode(s) //nolint:errcheck
	}
}

// analysisResult holds all file analysis data.
type analysisResult struct {
	sourceFiles []fileInfo
	testCount   int
	mdFiles     []string
	orphanMD    int
}

func analyzeDir(root string) ([]fileInfo, error) {
	result, err := analyzeAll(root)
	if err != nil {
		return nil, err
	}
	return result.sourceFiles, nil
}

func analyzeAll(root string) (*analysisResult, error) {
	result := &analysisResult{}
	var mdFiles []string

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

		// Track markdown files
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			mdFiles = append(mdFiles, path)
			return nil
		}

		// Only analyze .go files
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Count test files separately
		if strings.HasSuffix(path, "_test.go") {
			result.testCount++
			return nil
		}

		lines, err := countLines(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		result.sourceFiles = append(result.sourceFiles, fileInfo{path: path, lines: lines})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort source files by line count descending
	sort.Slice(result.sourceFiles, func(i, j int) bool {
		return result.sourceFiles[i].lines > result.sourceFiles[j].lines
	})

	// Analyze markdown links to find orphans
	result.mdFiles = mdFiles
	result.orphanMD = findOrphanMD(mdFiles)

	return result, nil
}

// findOrphanMD finds markdown files that aren't linked from anywhere.
func findOrphanMD(mdFiles []string) int {
	if len(mdFiles) == 0 {
		return 0
	}

	// Build set of all MD filenames (basename)
	mdSet := make(map[string]bool)
	for _, f := range mdFiles {
		mdSet[filepath.Base(f)] = true
	}

	// Track which files are linked
	linked := make(map[string]bool)

	// Scan each MD file for links to other MD files
	for _, mdPath := range mdFiles {
		content, err := os.ReadFile(mdPath)
		if err != nil {
			continue
		}

		// Simple link detection: [text](file.md) or [text](./path/file.md)
		text := string(content)
		for _, other := range mdFiles {
			base := filepath.Base(other)
			if strings.Contains(text, base) && other != mdPath {
				linked[base] = true
			}
		}
	}

	// Count orphans (not linked and not linking to others meaningfully)
	// README.md and CHANGELOG.md are typically entry points, not orphans
	orphans := 0
	for _, f := range mdFiles {
		base := filepath.Base(f)
		lower := strings.ToLower(base)
		// Skip common entry point files
		if lower == "readme.md" || lower == "changelog.md" || lower == "license.md" {
			continue
		}
		if !linked[base] {
			orphans++
		}
	}

	return orphans
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
