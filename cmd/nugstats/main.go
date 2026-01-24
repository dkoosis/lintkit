// Command nugstats reports knowledge graph nugget statistics.
//
// Usage:
//
//	nugstats                      # default human output
//	nugstats -format=check        # lintkit-check JSON
//	nugstats -format=sarif        # SARIF format (placeholder)
//	nugstats -db=/path/to.db      # custom database path
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dkoosis/lintkit/pkg/check"
	_ "github.com/mattn/go-sqlite3"
)

// KindCount holds per-kind statistics.
type KindCount struct {
	Kind     string
	Count    int
	ThisWeek int
	Delta    int // this_week - last_week
}

// WeeklyCount holds per-week statistics.
type WeeklyCount struct {
	Week  string
	Added int
}

// nuggetStats holds the raw statistics from the database.
type nuggetStats struct {
	Total         int
	TotalDelta    int // this_week_created - last_week_created
	PreviousTotal int // total from previous week snapshot
	ByKind        []KindCount
	Weekly        []WeeklyCount
}

func main() {
	dbPath := flag.String("db", "", "path to knowledge.db (default: ~/.orca/knowledge.db)")
	format := flag.String("format", "human", "output format: sarif, check, human")
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	stats, err := gatherStats(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "sarif":
		outputSARIF(stats)
	case "check":
		outputCheck(stats)
	default:
		outputHuman(stats)
	}
}

func openDB(path string) (*sql.DB, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".orca", "knowledge.db")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("knowledge database not found: %s", path)
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

func gatherStats(db *sql.DB) (*nuggetStats, error) {
	stats := &nuggetStats{}

	// Check if nugs table exists
	var tableName string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='nugs'`).Scan(&tableName)
	if errors.Is(err, sql.ErrNoRows) {
		// No nugs table - return empty stats
		return stats, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check nugs table: %w", err)
	}

	// Get counts by kind with week-over-week delta
	rows, err := db.Query(`
		SELECT
			k,
			COUNT(*) as current,
			SUM(CASE WHEN created > strftime('%s', 'now', '-7 days') THEN 1 ELSE 0 END) as this_week,
			SUM(CASE WHEN created > strftime('%s', 'now', '-14 days') AND created <= strftime('%s', 'now', '-7 days') THEN 1 ELSE 0 END) as last_week
		FROM nugs
		GROUP BY k
		ORDER BY current DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query kinds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var totalThisWeek, totalLastWeek int
	for rows.Next() {
		var kind string
		var count, thisWeek, lastWeek int
		if err := rows.Scan(&kind, &count, &thisWeek, &lastWeek); err != nil {
			return nil, fmt.Errorf("scan kind: %w", err)
		}
		stats.ByKind = append(stats.ByKind, KindCount{
			Kind:     kind,
			Count:    count,
			ThisWeek: thisWeek,
			Delta:    thisWeek - lastWeek,
		})
		stats.Total += count
		totalThisWeek += thisWeek
		totalLastWeek += lastWeek
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kinds: %w", err)
	}

	// Calculate total delta
	stats.TotalDelta = totalThisWeek - totalLastWeek
	stats.PreviousTotal = totalLastWeek

	// Get weekly counts for past 8 weeks
	rows, err = db.Query(`
		SELECT
			strftime('%Y-W%W', datetime(created, 'unixepoch')) as week,
			COUNT(*) as added
		FROM nugs
		WHERE created > strftime('%s', 'now', '-56 days')
			AND created IS NOT NULL
			AND created > 0
		GROUP BY week
		HAVING week IS NOT NULL
		ORDER BY week DESC
		LIMIT 8
	`)
	if err != nil {
		return nil, fmt.Errorf("query weekly: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var week string
		var added int
		if err := rows.Scan(&week, &added); err != nil {
			return nil, fmt.Errorf("scan weekly: %w", err)
		}
		stats.Weekly = append(stats.Weekly, WeeklyCount{
			Week:  week,
			Added: added,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate weekly: %w", err)
	}

	return stats, nil
}

func buildReport(stats *nuggetStats) *check.Report {
	report := check.NewReport("nugstats")

	// Add metrics
	report.Metrics = []check.Metric{
		{Name: "total", Value: float64(stats.Total), Unit: "nuggets"},
	}

	// Add kind breakdown as additional metrics
	for _, k := range stats.ByKind {
		report.Metrics = append(report.Metrics, check.Metric{
			Name:  k.Kind,
			Value: float64(k.Count),
		})
	}

	// Build trend from weekly data (oldest to newest for sparkline)
	if len(stats.Weekly) > 0 {
		// Weekly is newest-first, reverse for sparkline
		trend := make([]int, len(stats.Weekly))
		for i, w := range stats.Weekly {
			trend[len(stats.Weekly)-1-i] = w.Added
		}
		report.Trend = trend
	}

	// Add items for top kinds
	for _, k := range stats.ByKind {
		sev := check.SeverityInfo
		delta := ""
		if k.Delta > 0 {
			delta = fmt.Sprintf("+%d this week", k.Delta)
		} else if k.Delta < 0 {
			delta = fmt.Sprintf("%d this week", k.Delta)
		}
		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    k.Kind,
			Value:    fmt.Sprintf("%d", k.Count),
			Message:  delta,
		})
	}

	// Set summary and status
	if stats.Total == 0 {
		report.Status = check.StatusWarn
		report.Summary = "No nuggets found in knowledge graph"
	} else {
		report.Status = check.StatusPass
		deltaStr := ""
		if stats.TotalDelta > 0 {
			deltaStr = fmt.Sprintf(" (+%d this week)", stats.TotalDelta)
		} else if stats.TotalDelta < 0 {
			deltaStr = fmt.Sprintf(" (%d this week)", stats.TotalDelta)
		}
		report.Summary = fmt.Sprintf("%d nuggets across %d kinds%s", stats.Total, len(stats.ByKind), deltaStr)
	}

	return report
}

func outputCheck(stats *nuggetStats) {
	report := buildReport(stats)
	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func outputHuman(stats *nuggetStats) {
	report := buildReport(stats)
	cfg := check.DefaultHumanConfig()
	cfg.Purpose = "Knowledge graph nugget statistics"
	report.WriteHuman(os.Stdout, cfg)
}

// sarifLog is a minimal SARIF structure for nugstats.
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name string `json:"name"`
}

type sarifResult struct {
	RuleID  string       `json:"ruleId"`
	Level   string       `json:"level"`
	Message sarifMessage `json:"message"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

func outputSARIF(stats *nuggetStats) {
	log := sarifLog{
		Schema:  "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{Name: "lintkit-nugstats"}},
		}},
	}

	// Sort kinds by count for consistent output
	kinds := make([]KindCount, len(stats.ByKind))
	copy(kinds, stats.ByKind)
	sort.Slice(kinds, func(i, j int) bool {
		return kinds[i].Count > kinds[j].Count
	})

	for _, k := range kinds {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID: "nugget-kind",
			Level:  "note",
			Message: sarifMessage{
				Text: fmt.Sprintf("Kind %q: %d nuggets", k.Kind, k.Count),
			},
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding SARIF: %v\n", err)
		os.Exit(1)
	}
}
