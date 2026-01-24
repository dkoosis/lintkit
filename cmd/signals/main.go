// Command signals analyzes tool call telemetry to identify improvement opportunities.
//
// Reports generated:
//   - Tool Adoption: Orca MCP % vs bash %, navigation overhead
//   - Search Effectiveness: Entropy, refinement rate, zero-result outcomes
//   - Productive Workflows: Search->Action chains
//
// Usage:
//
//	signals                       # default human output
//	signals -format=check         # lintkit-check JSON
//	signals -format=sarif         # SARIF format (placeholder)
//	signals -db=/path/to.db       # custom database path
//	signals -days=30              # analyze last N days (default: 30)
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dkoosis/lintkit/pkg/check"
	_ "github.com/mattn/go-sqlite3"
)

// Report contains all telemetry signal analysis.
type Report struct {
	Timestamp   string            `json:"timestamp"`
	Period      string            `json:"period"`
	TotalCalls  int               `json:"total_calls"`
	Sessions    int               `json:"sessions"`
	Adoption    AdoptionMetrics   `json:"adoption"`
	Entropy     []EntropyMetric   `json:"entropy"`
	SelfRepeat  SelfRepeatMetrics `json:"self_repeat"`
	ZeroResults ZeroResultMetrics `json:"zero_results"`
	Workflows   []WorkflowMetric  `json:"workflows"`
	Warnings    []string          `json:"warnings,omitempty"`
}

// AdoptionMetrics tracks tool category usage.
type AdoptionMetrics struct {
	OrcaMCPPercent  float64 `json:"orca_mcp_percent"`
	BashPercent     float64 `json:"bash_percent"`
	GitOpsPercent   float64 `json:"git_ops_percent"`
	NavigationPct   float64 `json:"navigation_percent"` // cd overhead
	OrcaMCPCount    int     `json:"orca_mcp_count"`
	BashCount       int     `json:"bash_count"`
	NavigationCount int     `json:"navigation_count"`
}

// EntropyMetric measures confusion after using a tool.
type EntropyMetric struct {
	Tool        string  `json:"tool"`
	Entropy     float64 `json:"entropy"`
	Transitions int     `json:"transitions"`
}

// SelfRepeatMetrics analyzes consecutive same-tool calls.
type SelfRepeatMetrics struct {
	SearchNugsRefinementPct float64 `json:"search_nugs_refinement_pct"`
	SearchNugsRetryPct      float64 `json:"search_nugs_retry_pct"`
	TotalRefinements        int     `json:"total_refinements"`
	TotalRetries            int     `json:"total_retries"`
}

// ZeroResultMetrics tracks what happens after failed searches.
type ZeroResultMetrics struct {
	ZeroResultCount   int     `json:"zero_result_count"`
	ZeroResultPercent float64 `json:"zero_result_percent"`
	RetryAfterZero    int     `json:"retry_after_zero"`
	SaveAfterZero     int     `json:"save_after_zero"` // Created new nug
	ExploreAfterZero  int     `json:"explore_after_zero"`
}

// WorkflowMetric tracks productive search->action patterns.
type WorkflowMetric struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
}

var orcaMCPTools = map[string]bool{
	"view_file_local": true, "search_nugs": true, "search_file_local": true,
	"nugs.save": true, "go_symbol": true, "replace_text_local": true,
	"find_symbol_go": true, "give_feedback": true, "list_workspace_tree": true,
	"rate_nug": true, "import_nugs": true, "export_kg_md": true,
	"export_kg_graph": true, "analyze_metrics": true, "get_metrics": true,
	"bash_tool_local": true, "delete_nug": true, "ts_symbol": true,
	"nugs.delete": true, "file": true, "read_slice": true,
}

var bashTools = map[string]bool{
	"cat": true, "ls": true, "grep": true, "rg": true, "find": true,
	"pwd": true, "echo": true, "mkdir": true, "rm": true, "cp": true,
	"mv": true, "touch": true, "head": true, "tail": true, "wc": true,
	"sort": true, "uniq": true, "awk": true, "sed": true,
}

func main() {
	dbPath := flag.String("db", "", "path to telemetry.db (default: ~/.orca/telemetry.db)")
	format := flag.String("format", "human", "output format: sarif, check, human")
	days := flag.Int("days", 30, "analyze last N days")
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	report, err := generateReport(db, *days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "sarif":
		outputSARIF(report)
	case "check":
		outputCheck(report)
	default:
		outputHuman(report)
	}
}

func openDB(path string) (*sql.DB, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".orca", "telemetry.db")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("telemetry database not found: %s", path)
	}
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return db, nil
}

func generateReport(db *sql.DB, days int) (*Report, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)

	report := &Report{
		Timestamp: time.Now().Format(time.RFC3339),
		Period:    fmt.Sprintf("last %d days", days),
	}

	// Basic counts
	if err := db.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT session_id)
		FROM orca_tool_calls
		WHERE timestamp >= ?`, cutoff).Scan(&report.TotalCalls, &report.Sessions); err != nil {
		return nil, fmt.Errorf("count query: %w", err)
	}

	// Adoption metrics
	if err := computeAdoption(db, cutoff, report); err != nil {
		return nil, fmt.Errorf("adoption: %w", err)
	}

	// Entropy metrics
	if err := computeEntropy(db, cutoff, report); err != nil {
		return nil, fmt.Errorf("entropy: %w", err)
	}

	// Self-repeat analysis
	if err := computeSelfRepeat(db, cutoff, report); err != nil {
		return nil, fmt.Errorf("self-repeat: %w", err)
	}

	// Zero-result outcomes
	if err := computeZeroResults(db, cutoff, report); err != nil {
		return nil, fmt.Errorf("zero-results: %w", err)
	}

	// Productive workflows
	if err := computeWorkflows(db, cutoff, report); err != nil {
		return nil, fmt.Errorf("workflows: %w", err)
	}

	return report, nil
}

func computeAdoption(db *sql.DB, cutoff string, report *Report) error {
	rows, err := db.Query(`
		SELECT tool, COUNT(*) as cnt
		FROM orca_tool_calls
		WHERE timestamp >= ?
		GROUP BY tool`, cutoff)
	if err != nil {
		return fmt.Errorf("query tool adoption: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var orcaCount, bashCount, navCount, gitCount, total int
	for rows.Next() {
		var tool string
		var count int
		if err := rows.Scan(&tool, &count); err != nil {
			return fmt.Errorf("scan tool adoption row: %w", err)
		}
		total += count

		if orcaMCPTools[tool] {
			orcaCount += count
		} else if bashTools[tool] {
			bashCount += count
		} else if tool == "cd" {
			navCount += count
		} else if tool == "git" || tool == "gh" {
			gitCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tool adoption rows: %w", err)
	}

	if total > 0 {
		report.Adoption = AdoptionMetrics{
			OrcaMCPPercent:  float64(orcaCount) * 100 / float64(total),
			BashPercent:     float64(bashCount) * 100 / float64(total),
			GitOpsPercent:   float64(gitCount) * 100 / float64(total),
			NavigationPct:   float64(navCount) * 100 / float64(total),
			OrcaMCPCount:    orcaCount,
			BashCount:       bashCount,
			NavigationCount: navCount,
		}
	}
	return nil
}

func computeEntropy(db *sql.DB, cutoff string, report *Report) error {
	// Calculate transition entropy for key tools
	tools := []string{"search_nugs", "view_file_local", "search_file_local", "find_symbol_go", "cd", "cat", "ls"}

	for _, tool := range tools {
		if err := computeToolEntropy(db, cutoff, tool, report); err != nil {
			continue // Skip tools that fail
		}
	}
	return nil
}

func computeToolEntropy(db *sql.DB, cutoff, tool string, report *Report) error {
	rows, err := db.Query(`
		WITH ordered_calls AS (
			SELECT session_id, tool,
				ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY timestamp) as seq
			FROM orca_tool_calls
			WHERE session_id IS NOT NULL AND timestamp >= ?
		),
		transitions AS (
			SELECT b.tool as next_tool, COUNT(*) as cnt
			FROM ordered_calls a
			JOIN ordered_calls b ON a.session_id = b.session_id AND a.seq = b.seq - 1
			WHERE a.tool = ?
			GROUP BY b.tool
		)
		SELECT next_tool, cnt FROM transitions`, cutoff, tool)
	if err != nil {
		return fmt.Errorf("query tool entropy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var total int
	counts := make(map[string]int)
	for rows.Next() {
		var nextTool string
		var cnt int
		if err := rows.Scan(&nextTool, &cnt); err != nil {
			return fmt.Errorf("scan tool entropy row: %w", err)
		}
		counts[nextTool] = cnt
		total += cnt
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tool entropy rows: %w", err)
	}

	if total > 0 {
		entropy := 0.0
		for _, cnt := range counts {
			p := float64(cnt) / float64(total)
			if p > 0 {
				entropy -= p * math.Log2(p)
			}
		}
		report.Entropy = append(report.Entropy, EntropyMetric{
			Tool:        tool,
			Entropy:     math.Round(entropy*100) / 100,
			Transitions: total,
		})
	}
	return nil
}

func computeSelfRepeat(db *sql.DB, cutoff string, report *Report) error {
	// For search_nugs: check if consecutive calls have same or different params
	row := db.QueryRow(`
		WITH ordered_calls AS (
			SELECT session_id, tool, params_json,
				LAG(params_json) OVER (PARTITION BY session_id ORDER BY timestamp) as prev_params,
				LAG(tool) OVER (PARTITION BY session_id ORDER BY timestamp) as prev_tool
			FROM orca_tool_calls
			WHERE session_id IS NOT NULL
				AND timestamp >= ?
				AND tool = 'search_nugs'
				AND params_json IS NOT NULL
				AND params_json NOT LIKE '%boot%'
		)
		SELECT
			COALESCE(SUM(CASE WHEN prev_tool = 'search_nugs' AND params_json = prev_params THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN prev_tool = 'search_nugs' AND params_json != prev_params THEN 1 ELSE 0 END), 0)
		FROM ordered_calls`, cutoff)

	var retries, refinements int
	if err := row.Scan(&retries, &refinements); err != nil {
		return fmt.Errorf("scan self-repeat totals: %w", err)
	}

	total := retries + refinements
	if total > 0 {
		report.SelfRepeat = SelfRepeatMetrics{
			SearchNugsRefinementPct: float64(refinements) * 100 / float64(total),
			SearchNugsRetryPct:      float64(retries) * 100 / float64(total),
			TotalRefinements:        refinements,
			TotalRetries:            retries,
		}
	}
	return nil
}

func computeZeroResults(db *sql.DB, cutoff string, report *Report) error {
	// Count zero-result searches and what follows
	row := db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN json_extract(result_json, '$.structuredContent.count') = 0 THEN 1 ELSE 0 END), 0)
		FROM orca_tool_calls
		WHERE tool = 'search_nugs'
			AND result_json IS NOT NULL
			AND timestamp >= ?`, cutoff)

	var total, zeroCount int
	if err := row.Scan(&total, &zeroCount); err != nil {
		return fmt.Errorf("scan zero-result totals: %w", err)
	}

	if total > 0 {
		report.ZeroResults.ZeroResultCount = zeroCount
		report.ZeroResults.ZeroResultPercent = float64(zeroCount) * 100 / float64(total)
	}

	// What follows zero results
	rows, err := db.Query(`
		WITH ordered_calls AS (
			SELECT session_id, tool, result_json,
				ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY timestamp) as seq
			FROM orca_tool_calls
			WHERE session_id IS NOT NULL AND timestamp >= ?
		)
		SELECT b.tool, COUNT(*)
		FROM ordered_calls a
		JOIN ordered_calls b ON a.session_id = b.session_id AND a.seq = b.seq - 1
		WHERE a.tool = 'search_nugs'
			AND a.result_json IS NOT NULL
			AND json_extract(a.result_json, '$.structuredContent.count') = 0
		GROUP BY b.tool`, cutoff)
	if err != nil {
		return fmt.Errorf("query zero-result transitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var tool string
		var count int
		if err := rows.Scan(&tool, &count); err != nil {
			return fmt.Errorf("scan zero-result transition: %w", err)
		}
		switch tool {
		case "search_nugs":
			report.ZeroResults.RetryAfterZero = count
		case "nugs.save":
			report.ZeroResults.SaveAfterZero = count
		default:
			report.ZeroResults.ExploreAfterZero += count
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate zero-result transitions: %w", err)
	}
	return nil
}

func computeWorkflows(db *sql.DB, cutoff string, report *Report) error {
	rows, err := db.Query(`
		WITH ordered_calls AS (
			SELECT session_id, tool,
				ROW_NUMBER() OVER (PARTITION BY session_id ORDER BY timestamp) as seq
			FROM orca_tool_calls
			WHERE session_id IS NOT NULL AND timestamp >= ?
		)
		SELECT a.tool || ' -> ' || b.tool as pattern, COUNT(*) as cnt
		FROM ordered_calls a
		JOIN ordered_calls b ON a.session_id = b.session_id AND a.seq = b.seq - 1
		WHERE a.tool IN ('search_nugs', 'search_file_local', 'view_file_local', 'find_symbol_go', 'go_symbol')
			AND b.tool IN ('nugs.save', 'replace_text_local', 'edit_symbol_go', 'give_feedback', 'rate_nug', 'nugs.delete')
		GROUP BY pattern
		ORDER BY cnt DESC
		LIMIT 10`, cutoff)
	if err != nil {
		return fmt.Errorf("query workflow patterns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var pattern string
		var count int
		if err := rows.Scan(&pattern, &count); err != nil {
			return fmt.Errorf("scan workflow pattern: %w", err)
		}
		report.Workflows = append(report.Workflows, WorkflowMetric{
			Pattern: pattern,
			Count:   count,
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate workflow patterns: %w", err)
	}
	return nil
}

// getDailyCalls returns daily call counts for trend sparkline.
func getDailyCalls(db *sql.DB, days int) ([]int, error) {
	rows, err := db.Query(`
		SELECT date(timestamp) as day, COUNT(*) as cnt
		FROM orca_tool_calls
		WHERE timestamp >= date('now', '-' || ? || ' days')
		GROUP BY day
		ORDER BY day ASC`, days)
	if err != nil {
		return nil, fmt.Errorf("query daily calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var counts []int
	for rows.Next() {
		var day string
		var cnt int
		if err := rows.Scan(&day, &cnt); err != nil {
			return nil, fmt.Errorf("scan daily call: %w", err)
		}
		counts = append(counts, cnt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily calls: %w", err)
	}
	return counts, nil
}

func buildCheckReport(r *Report, db *sql.DB, days int) *check.Report {
	report := check.NewReport("signals")

	// Add metrics
	report.Metrics = []check.Metric{
		{Name: "total_calls", Value: float64(r.TotalCalls), Unit: "calls"},
		{Name: "sessions", Value: float64(r.Sessions), Unit: "sessions"},
		{Name: "orca_mcp", Value: r.Adoption.OrcaMCPPercent, Unit: "%"},
		{Name: "bash", Value: r.Adoption.BashPercent, Unit: "%"},
		{Name: "navigation", Value: r.Adoption.NavigationPct, Unit: "%"},
	}

	// Add entropy metrics
	for _, e := range r.Entropy {
		report.Metrics = append(report.Metrics, check.Metric{
			Name:  fmt.Sprintf("entropy_%s", e.Tool),
			Value: e.Entropy,
		})
	}

	// Build trend from daily data
	if dailyCounts, err := getDailyCalls(db, days); err == nil && len(dailyCounts) > 0 {
		report.Trend = dailyCounts
	}

	// Add items for concerning metrics
	if r.Adoption.OrcaMCPPercent < 40 {
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityWarn,
			Label:    "Low Orca adoption",
			Value:    fmt.Sprintf("%.1f%%", r.Adoption.OrcaMCPPercent),
			Message:  "Consider using more Orca MCP tools instead of bash",
		})
	}

	if r.Adoption.NavigationPct > 20 {
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityWarn,
			Label:    "High cd overhead",
			Value:    fmt.Sprintf("%.1f%%", r.Adoption.NavigationPct),
			Message:  "Too many cd commands - use absolute paths or workspace-aware tools",
		})
	}

	if r.SelfRepeat.SearchNugsRetryPct > 10 {
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityWarn,
			Label:    "High retry rate",
			Value:    fmt.Sprintf("%.1f%%", r.SelfRepeat.SearchNugsRetryPct),
			Message:  "Repeated same-params searches indicate confusion",
		})
	}

	// Add entropy items (high entropy = confusion)
	for _, e := range r.Entropy {
		if e.Entropy > 3.0 {
			report.Items = append(report.Items, check.Item{
				Severity: check.SeverityInfo,
				Label:    fmt.Sprintf("High entropy: %s", e.Tool),
				Value:    fmt.Sprintf("%.2f", e.Entropy),
				Message:  "Many different next-steps after this tool",
			})
		}
	}

	// Add workflow patterns as info items
	for _, w := range r.Workflows {
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityInfo,
			Label:    w.Pattern,
			Value:    fmt.Sprintf("%d", w.Count),
			Message:  "productive workflow",
		})
	}

	// Set summary and status
	avgEnt := avgEntropy(r.Entropy)
	report.Summary = fmt.Sprintf("Orca %.0f%% | Entropy avg %.2f | %d sessions",
		r.Adoption.OrcaMCPPercent, avgEnt, r.Sessions)

	// Determine status
	if r.Adoption.OrcaMCPPercent < 40 || r.Adoption.NavigationPct > 20 || r.SelfRepeat.SearchNugsRetryPct > 10 {
		report.Status = check.StatusWarn
	} else {
		report.Status = check.StatusPass
	}

	return report
}

func outputCheck(r *Report) {
	// Re-open DB for trend data (not ideal but keeps function signature simple)
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".orca", "telemetry.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		// Build report without trend
		report := buildCheckReportNoTrend(r)
		if err := report.Write(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
			os.Exit(1)
		}
		return
	}
	defer func() { _ = db.Close() }()

	report := buildCheckReport(r, db, 30)
	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func buildCheckReportNoTrend(r *Report) *check.Report {
	report := check.NewReport("signals")
	report.Metrics = []check.Metric{
		{Name: "total_calls", Value: float64(r.TotalCalls), Unit: "calls"},
		{Name: "sessions", Value: float64(r.Sessions), Unit: "sessions"},
		{Name: "orca_mcp", Value: r.Adoption.OrcaMCPPercent, Unit: "%"},
	}
	report.Summary = fmt.Sprintf("Orca %.0f%% | %d sessions", r.Adoption.OrcaMCPPercent, r.Sessions)
	report.Status = check.StatusPass
	return report
}

func outputHuman(r *Report) {
	// Re-open DB for trend data
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".orca", "telemetry.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		outputHumanNoTrend(r)
		return
	}
	defer func() { _ = db.Close() }()

	report := buildCheckReport(r, db, 30)
	cfg := check.DefaultHumanConfig()
	cfg.Purpose = fmt.Sprintf("Telemetry signal analysis (%s)", r.Period)
	report.WriteHuman(os.Stdout, cfg)

	// Print additional detail sections not captured in check format
	fmt.Println()
	fmt.Println("=== Tool Adoption ===")
	fmt.Printf("  Orca MCP:   %5.1f%% (%d calls)\n", r.Adoption.OrcaMCPPercent, r.Adoption.OrcaMCPCount)
	fmt.Printf("  Bash:       %5.1f%% (%d calls)\n", r.Adoption.BashPercent, r.Adoption.BashCount)
	fmt.Printf("  Navigation: %5.1f%% (%d calls) [cd overhead]\n", r.Adoption.NavigationPct, r.Adoption.NavigationCount)

	fmt.Println("\n=== Entropy (lower = less confusion) ===")
	for _, e := range r.Entropy {
		bar := strings.Repeat("|", int(e.Entropy*3))
		fmt.Printf("  %-18s %4.2f %s\n", e.Tool, e.Entropy, bar)
	}

	fmt.Println("\n=== Self-Repeat (search_nugs) ===")
	fmt.Printf("  Refinement: %5.1f%% (%d) [params changed - healthy]\n",
		r.SelfRepeat.SearchNugsRefinementPct, r.SelfRepeat.TotalRefinements)
	fmt.Printf("  Retry:      %5.1f%% (%d) [same params - frustrated]\n",
		r.SelfRepeat.SearchNugsRetryPct, r.SelfRepeat.TotalRetries)

	fmt.Println("\n=== Zero-Result Outcomes ===")
	fmt.Printf("  Zero results: %5.1f%% (%d searches)\n",
		r.ZeroResults.ZeroResultPercent, r.ZeroResults.ZeroResultCount)
	fmt.Printf("  After zero: retry=%d, save_new=%d, explore=%d\n",
		r.ZeroResults.RetryAfterZero, r.ZeroResults.SaveAfterZero, r.ZeroResults.ExploreAfterZero)
}

func outputHumanNoTrend(r *Report) {
	fmt.Printf("Telemetry Signals Report (%s)\n", r.Period)
	fmt.Printf("Total: %d calls across %d sessions\n\n", r.TotalCalls, r.Sessions)

	fmt.Println("=== Tool Adoption ===")
	fmt.Printf("  Orca MCP:   %5.1f%% (%d calls)\n", r.Adoption.OrcaMCPPercent, r.Adoption.OrcaMCPCount)
	fmt.Printf("  Bash:       %5.1f%% (%d calls)\n", r.Adoption.BashPercent, r.Adoption.BashCount)
	fmt.Printf("  Navigation: %5.1f%% (%d calls) [cd overhead]\n", r.Adoption.NavigationPct, r.Adoption.NavigationCount)

	fmt.Println("\n=== Entropy (lower = less confusion) ===")
	for _, e := range r.Entropy {
		bar := strings.Repeat("|", int(e.Entropy*3))
		fmt.Printf("  %-18s %4.2f %s\n", e.Tool, e.Entropy, bar)
	}

	fmt.Println("\n=== Self-Repeat (search_nugs) ===")
	fmt.Printf("  Refinement: %5.1f%% (%d) [params changed - healthy]\n",
		r.SelfRepeat.SearchNugsRefinementPct, r.SelfRepeat.TotalRefinements)
	fmt.Printf("  Retry:      %5.1f%% (%d) [same params - frustrated]\n",
		r.SelfRepeat.SearchNugsRetryPct, r.SelfRepeat.TotalRetries)

	fmt.Println("\n=== Zero-Result Outcomes ===")
	fmt.Printf("  Zero results: %5.1f%% (%d searches)\n",
		r.ZeroResults.ZeroResultPercent, r.ZeroResults.ZeroResultCount)
	fmt.Printf("  After zero: retry=%d, save_new=%d, explore=%d\n",
		r.ZeroResults.RetryAfterZero, r.ZeroResults.SaveAfterZero, r.ZeroResults.ExploreAfterZero)

	fmt.Println("\n=== Productive Workflows (search -> action) ===")
	for _, w := range r.Workflows {
		fmt.Printf("  %-40s %d\n", w.Pattern, w.Count)
	}
}

// sarifLog is a minimal SARIF structure for signals.
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

func outputSARIF(r *Report) {
	log := sarifLog{
		Schema:  "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{Name: "lintkit-signals"}},
		}},
	}

	// Add adoption metrics as results
	log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
		RuleID: "tool-adoption",
		Level:  "note",
		Message: sarifMessage{
			Text: fmt.Sprintf("Orca MCP: %.1f%%, Bash: %.1f%%, Navigation: %.1f%%",
				r.Adoption.OrcaMCPPercent, r.Adoption.BashPercent, r.Adoption.NavigationPct),
		},
	})

	// Add warnings for concerning metrics
	if r.Adoption.OrcaMCPPercent < 40 {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID:  "low-adoption",
			Level:   "warning",
			Message: sarifMessage{Text: fmt.Sprintf("Low Orca adoption: %.1f%%", r.Adoption.OrcaMCPPercent)},
		})
	}
	if r.Adoption.NavigationPct > 20 {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID:  "high-navigation",
			Level:   "warning",
			Message: sarifMessage{Text: fmt.Sprintf("High cd overhead: %.1f%%", r.Adoption.NavigationPct)},
		})
	}
	if r.SelfRepeat.SearchNugsRetryPct > 10 {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID:  "high-retry",
			Level:   "warning",
			Message: sarifMessage{Text: fmt.Sprintf("High retry rate: %.1f%%", r.SelfRepeat.SearchNugsRetryPct)},
		})
	}

	// Add entropy metrics
	for _, e := range r.Entropy {
		log.Runs[0].Results = append(log.Runs[0].Results, sarifResult{
			RuleID: "entropy",
			Level:  "note",
			Message: sarifMessage{
				Text: fmt.Sprintf("Tool %q entropy: %.2f (%d transitions)", e.Tool, e.Entropy, e.Transitions),
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

func avgEntropy(metrics []EntropyMetric) float64 {
	if len(metrics) == 0 {
		return 0
	}
	sum := 0.0
	for _, m := range metrics {
		sum += m.Entropy
	}
	return sum / float64(len(metrics))
}
