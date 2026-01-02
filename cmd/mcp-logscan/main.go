// Command mcp-logscan scans MCP server logs for errors and warnings.
//
// Parses both JSON (Claude Desktop) and text (Claude Code) log formats,
// filtering for ERROR/WARN level messages. Useful for debugging MCP servers.
//
// Usage:
//
//	mcp-logscan                    # default table output, last 3 days
//	mcp-logscan -format=dashboard  # JSON for fo dashboard
//	mcp-logscan -days=7            # scan past N days
//
// Scans: ~/Library/Logs/Claude/mcp-server-*.log
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Msg     string `json:"msg"`
	Service string `json:"service"`
	Panic   string `json:"panic,omitempty"`
	Error   string `json:"error,omitempty"`
	ID      string `json:"id,omitempty"`
}

type ErrorSummary struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	LogFile string `json:"log_file,omitempty"`
}

type Report struct {
	Timestamp  string         `json:"timestamp"`
	LogFiles   []string       `json:"log_files"`
	ErrorCount int            `json:"error_count"`
	WarnCount  int            `json:"warn_count"`
	Errors     []ErrorSummary `json:"errors"`
}

func main() {
	format := flag.String("format", "table", "output format: table, dashboard")
	days := flag.Int("days", 3, "scan logs from past N days")
	flag.Parse()

	report := scanLogs(*days)

	switch *format {
	case "dashboard":
		outputDashboard(report)
	default:
		outputTable(report)
	}
}

func scanLogs(days int) *Report {
	report := &Report{
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Log locations
	logDirs := []string{
		filepath.Join(os.Getenv("HOME"), "Library", "Logs", "Claude"),
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	for _, dir := range logDirs {
		files, err := filepath.Glob(filepath.Join(dir, "mcp-server-*.log"))
		if err != nil {
			continue
		}

		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}

			baseName := filepath.Base(file)
			report.LogFiles = append(report.LogFiles, baseName)
			scanFile(file, baseName, report, cutoff)
		}
	}

	return report
}

func scanFile(path, logFile string, report *Report, cutoff time.Time) {
	f, err := os.Open(path) //nolint:gosec // G304: path from filepath.Walk
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	// Pattern for non-JSON log lines (Claude Code format)
	ccPattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T.+\[orca\]\s+\[(error|warn)\]`)

	// Use a reader that can handle very long lines (MCP payloads can be huge)
	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break // EOF or error
		}
		line = strings.TrimSpace(line)

		// Try JSON format first (Claude Desktop)
		if strings.HasPrefix(line, "{") {
			var entry LogEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}

			// Check time - handle various formats
			var t time.Time
			var parseErr error
			for _, layout := range []string{
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02T15:04:05.999999Z",
			} {
				t, parseErr = time.Parse(layout, entry.Time)
				if parseErr == nil {
					break
				}
			}
			if t.IsZero() || t.Before(cutoff) {
				continue
			}

			// Filter for errors/warnings
			switch entry.Level {
			case "ERROR":
				report.ErrorCount++
				report.Errors = append(report.Errors, ErrorSummary{
					Time:    entry.Time,
					Level:   "ERROR",
					Message: entry.Msg,
					Detail:  firstNonEmpty(entry.Error, entry.Panic),
					LogFile: logFile,
				})
			case "WARN":
				report.WarnCount++
				// Only include panics as errors
				if entry.Panic != "" {
					report.Errors = append(report.Errors, ErrorSummary{
						Time:    entry.Time,
						Level:   "WARN",
						Message: entry.Msg,
						Detail:  entry.Panic,
						LogFile: logFile,
					})
				}
			}
			continue
		}

		// Try Claude Code format: 2025-12-16T17:55:06.038Z [orca] [error] ...
		if ccPattern.MatchString(line) {
			// Extract level and message
			if strings.Contains(line, "[error]") {
				report.ErrorCount++
				msg := extractMessage(line)
				report.Errors = append(report.Errors, ErrorSummary{
					Time:    extractTimestamp(line),
					Level:   "ERROR",
					Message: msg,
					LogFile: logFile,
				})
			} else if strings.Contains(line, "[warn]") {
				report.WarnCount++
			}
		}
	}
}

func extractTimestamp(line string) string {
	if len(line) >= 24 {
		return line[:24]
	}
	return ""
}

func extractMessage(line string) string {
	// Find last ] and return rest
	idx := strings.LastIndex(line, "]")
	if idx > 0 && idx < len(line)-1 {
		return strings.TrimSpace(line[idx+1:])
	}
	return line
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func outputDashboard(report *Report) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
}

func outputTable(report *Report) {
	if report.ErrorCount == 0 && report.WarnCount == 0 {
		fmt.Println("✓ No errors")
		return
	}

	fmt.Printf("Recent Errors (%d errors, %d warnings)\n", report.ErrorCount, report.WarnCount)

	// Group errors by log file
	byFile := make(map[string][]ErrorSummary)
	for _, e := range report.Errors {
		byFile[e.LogFile] = append(byFile[e.LogFile], e)
	}

	// Process each file
	for _, logFile := range report.LogFiles {
		errors := byFile[logFile]
		if len(errors) == 0 {
			continue
		}

		fmt.Printf("\n    ~/Library/Logs/Claude/%s\n", logFile)

		// Group by date within file
		byDate := make(map[string][]ErrorSummary)
		for _, e := range errors {
			date := extractDate(e.Time)
			byDate[date] = append(byDate[date], e)
		}

		// Get sorted dates (reverse chronological)
		var dates []string
		for date := range byDate {
			dates = append(dates, date)
		}
		sortDatesDesc(dates)

		// Print errors grouped by date
		for _, date := range dates {
			fmt.Printf("    %s\n", date)
			for _, e := range byDate[date] {
				fmt.Printf("    ✗ %s\n", e.Message)
				if e.Detail != "" {
					fmt.Printf("      %s\n", e.Detail)
				}
			}
		}
	}
}

func extractDate(timestamp string) string {
	if len(timestamp) >= 10 {
		return timestamp[:10]
	}
	return "unknown"
}

func sortDatesDesc(dates []string) {
	// Simple bubble sort in descending order
	for i := 0; i < len(dates); i++ {
		for j := i + 1; j < len(dates); j++ {
			if dates[i] < dates[j] {
				dates[i], dates[j] = dates[j], dates[i]
			}
		}
	}
}
