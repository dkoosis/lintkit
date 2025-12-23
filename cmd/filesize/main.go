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
	Deltas    DashboardDeltas  `json:"deltas"`
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

// DashboardDeltas contains changes from historical snapshots.
type DashboardDeltas struct {
	Day   MetricDeltas `json:"day"`   // 1 day ago
	Week  MetricDeltas `json:"week"`  // 1 week ago
	Month MetricDeltas `json:"month"` // 1 month ago
}

// MetricDeltas holds delta values for each metric.
type MetricDeltas struct {
	Total     int `json:"total"`
	Green     int `json:"green"`
	Yellow    int `json:"yellow"`
	Red       int `json:"red"`
	TestFiles int `json:"test_files"`
	MDFiles   int `json:"md_files"`
	OrphanMD  int `json:"orphan_md"`
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

// excludePatterns holds MD path patterns to exclude from counts.
var excludePatterns []string

func main() {
	dir := flag.String("dir", ".", "directory to analyze")
	format := flag.String("format", "sarif", "output format: sarif, text, dashboard")
	top := flag.Int("top", 0, "limit output to top N files (0=all)")
	snapshotFile := flag.String("snapshots", "", "path to snapshots JSONL file for history (dashboard format only)")
	excludeMD := flag.String("exclude-md", "", "comma-separated path patterns to exclude from MD counts (e.g., '**/templates/**,**/testdata/**')")
	flag.Parse()

	if *excludeMD != "" {
		excludePatterns = strings.Split(*excludeMD, ",")
	}

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

	// Load snapshots and calculate deltas
	var history []HistoryEntry
	var deltas DashboardDeltas
	if snapshotFile != "" {
		snapshots := loadSnapshots(snapshotFile)
		history = buildHistory(snapshots)
		deltas = calculateDeltas(snapshots, metrics)
	}

	output := DashboardOutput{
		Timestamp: time.Now(),
		Metrics:   metrics,
		Deltas:    deltas,
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

// calculateDeltas finds historical snapshots and computes deltas for 1 day, 1 week, 1 month ago.
func calculateDeltas(snapshots []snapshot, current DashboardMetrics) DashboardDeltas {
	now := time.Now()

	// Find closest snapshot to each time period
	dayAgo := findClosestSnapshot(snapshots, now.AddDate(0, 0, -1))
	weekAgo := findClosestSnapshot(snapshots, now.AddDate(0, 0, -7))
	monthAgo := findClosestSnapshot(snapshots, now.AddDate(0, -1, 0))

	return DashboardDeltas{
		Day:   computeMetricDeltas(current, dayAgo),
		Week:  computeMetricDeltas(current, weekAgo),
		Month: computeMetricDeltas(current, monthAgo),
	}
}

// findClosestSnapshot finds the snapshot closest to the target time (within 36 hours).
func findClosestSnapshot(snapshots []snapshot, target time.Time) *snapshot {
	if len(snapshots) == 0 {
		return nil
	}

	var closest *snapshot
	minDiff := time.Duration(36 * time.Hour) // Max tolerance: 36 hours

	for i := range snapshots {
		s := &snapshots[i]
		diff := s.Ts.Sub(target)
		if diff < 0 {
			diff = -diff
		}
		if diff < minDiff {
			minDiff = diff
			closest = s
		}
	}

	return closest
}

// computeMetricDeltas calculates the difference between current and previous metrics.
func computeMetricDeltas(current DashboardMetrics, prev *snapshot) MetricDeltas {
	if prev == nil {
		return MetricDeltas{} // No historical data
	}

	return MetricDeltas{
		Total:     current.Total - prev.Total,
		Green:     current.Green - prev.Green,
		Yellow:    current.Yellow - prev.Yellow,
		Red:       current.Red - prev.Red,
		TestFiles: current.TestFiles - prev.TestFiles,
		MDFiles:   current.MDFiles - prev.MDFiles,
		OrphanMD:  current.OrphanMD - prev.OrphanMD,
	}
}

// loadSnapshots loads all snapshots from the JSONL file.
func loadSnapshots(path string) []snapshot {
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
	return snapshots
}

// buildHistory groups snapshots by week and returns the last 8 weeks.
func buildHistory(snapshots []snapshot) []HistoryEntry {
	if len(snapshots) == 0 {
		return nil
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

		// Track markdown files (unless excluded)
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			if !isExcludedMD(path) {
				mdFiles = append(mdFiles, path)
			}
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

// isExcludedMD checks if an MD file path matches any exclude pattern.
func isExcludedMD(path string) bool {
	for _, pattern := range excludePatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		// Convert glob pattern to work with filepath.Match
		// Handle ** patterns by checking if path contains the non-** parts
		if strings.Contains(pattern, "**") {
			// Split on ** and check if all parts are present in order
			parts := strings.Split(pattern, "**")
			remaining := path
			matched := true
			for _, part := range parts {
				part = strings.Trim(part, "/")
				if part == "" {
					continue
				}
				idx := strings.Index(remaining, part)
				if idx == -1 {
					matched = false
					break
				}
				remaining = remaining[idx+len(part):]
			}
			if matched {
				return true
			}
		} else if matched, _ := filepath.Match(pattern, path); matched {
			return true
		} else if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
	}
	return false
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
