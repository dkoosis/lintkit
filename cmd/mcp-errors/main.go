// Command mcp-errors scans Claude MCP logs for orca-related errors.
//
// Usage:
//
//	mcp-errors                    # default table output
//	mcp-errors -format=dashboard  # JSON for fo dashboard
//	mcp-errors -days=7            # scan past N days (default: 1)
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
	days := flag.Int("days", 1, "scan logs from past N days")
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
		files, err := filepath.Glob(filepath.Join(dir, "mcp-server-orca*.log"))
		if err != nil {
			continue
		}

		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}

			report.LogFiles = append(report.LogFiles, filepath.Base(file))
			scanFile(file, report, cutoff)
		}
	}

	return report
}

func scanFile(path string, report *Report, cutoff time.Time) {
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
		fmt.Println("MCP Logs: ✓ No errors")
		return
	}

	fmt.Printf("MCP Logs: %d errors, %d warnings\n", report.ErrorCount, report.WarnCount)
	fmt.Println("═══════════════════════════════════════")

	if len(report.Errors) == 0 {
		return
	}

	fmt.Println()
	// Show most recent 10 errors
	shown := 0
	for i := len(report.Errors) - 1; i >= 0 && shown < 10; i-- {
		e := report.Errors[i]
		fmt.Printf("[%s] %s\n", e.Level, e.Message)
		if e.Detail != "" {
			// Truncate long details
			detail := e.Detail
			if len(detail) > 80 {
				detail = detail[:77] + "..."
			}
			fmt.Printf("         %s\n", detail)
		}
		shown++
	}

	if len(report.Errors) > 10 {
		fmt.Printf("\n... and %d more\n", len(report.Errors)-10)
	}
}
