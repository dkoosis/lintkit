package dbsanity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Baseline describes expected row counts per table.
type Baseline struct {
	Tables map[string]int64 `json:"tables"`
}

// LoadBaseline loads a baseline definition from a JSON file.
func LoadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, err
	}

	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, err
	}

	if b.Tables == nil {
		return Baseline{}, errors.New("baseline missing tables map")
	}

	return b, nil
}

// CheckDatabase compares the current counts in the database against the baseline
// and returns SARIF results for any tables whose drift exceeds the threshold.
func CheckDatabase(ctx context.Context, dbPath string, baseline Baseline, threshold float64) ([]sarif.Result, error) {
	existingTables, err := listTables(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	var results []sarif.Result
	for table, baselineCount := range baseline.Tables {
		currentCount, ok := existingTables[table]
		missing := !ok
		if !missing {
			count, err := countRows(ctx, dbPath, table)
			if err != nil {
				return nil, err
			}
			currentCount = count
		}

		diffPct := percentageDiff(baselineCount, currentCount)
		if math.Abs(diffPct) > threshold {
			msg := fmt.Sprintf("Table %s drifted: baseline=%d current=%d diff=%.2f%%", table, baselineCount, currentCount, diffPct)
			if missing {
				msg = fmt.Sprintf("Table %s missing: baseline=%d current=0 diff=%.2f%%", table, baselineCount, diffPct)
			}

			results = append(results, sarif.Result{
				RuleID: "db-row-drift",
				Level:  "error",
				Message: sarif.Message{
					Text: msg,
				},
				Locations: []sarif.Location{
					{
						PhysicalLocation: sarif.PhysicalLocation{
							ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
						},
					},
				},
			})
		}
	}

	return results, nil
}

func listTables(ctx context.Context, dbPath string) (map[string]int64, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	tables := make(map[string]int64)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		tables[name] = 0
	}

	return tables, nil
}

func countRows(ctx context.Context, dbPath, table string) (int64, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM \"%s\";", table)
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, query)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	countStr := strings.TrimSpace(string(output))
	if countStr == "" {
		return 0, fmt.Errorf("no count returned for table %s", table)
	}

	count, err := strconv.ParseInt(countStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func percentageDiff(baseline, current int64) float64 {
	denom := baseline
	if denom == 0 {
		denom = 1
	}
	return 100 * float64(current-baseline) / float64(denom)
}

// BuildLog constructs a SARIF log for the provided results.
func BuildLog(results []sarif.Result) *sarif.Log {
	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-dbsanity"}},
	}

	if len(results) > 0 {
		run.Results = results
	}

	log.Runs = append(log.Runs, run)
	return log
}

// RunChecks executes all configured checks against the database.
func RunChecks(ctx context.Context, dbPath string, cfg Config) (map[string]CheckResult, error) {
	results := make(map[string]CheckResult)

	for _, check := range cfg.Checks {
		result, err := executeCheck(ctx, dbPath, check)
		if err != nil {
			return nil, fmt.Errorf("check %q failed: %w", check.Name, err)
		}
		results[check.Name] = result
	}

	return results, nil
}

func executeCheck(ctx context.Context, dbPath string, check Check) (CheckResult, error) {
	switch check.Type {
	case CheckTypeScalar:
		return executeScalarCheck(ctx, dbPath, check.Query)
	case CheckTypeBreakdown:
		return executeBreakdownCheck(ctx, dbPath, check.Query)
	default:
		return CheckResult{}, fmt.Errorf("unknown check type: %s", check.Type)
	}
}

func executeScalarCheck(ctx context.Context, dbPath, query string) (CheckResult, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, query)
	output, err := cmd.Output()
	if err != nil {
		return CheckResult{}, err
	}

	valStr := strings.TrimSpace(string(output))
	if valStr == "" {
		return CheckResult{Scalar: 0}, nil
	}

	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return CheckResult{}, fmt.Errorf("parse scalar result: %w", err)
	}

	return CheckResult{Scalar: val}, nil
}

func executeBreakdownCheck(ctx context.Context, dbPath, query string) (CheckResult, error) {
	cmd := exec.CommandContext(ctx, "sqlite3", dbPath, query)
	output, err := cmd.Output()
	if err != nil {
		return CheckResult{}, err
	}

	breakdown := make(map[string]int64)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Expected format: key|count
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])

		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			continue
		}

		breakdown[key] = val
	}

	return CheckResult{Breakdown: breakdown}, nil
}

// CompareWithHistory generates SARIF results comparing current results with history.
func CompareWithHistory(dbPath string, current map[string]CheckResult, history *History, currentWeek string) []sarif.Result {
	var results []sarif.Result

	// Always emit informational results for current values
	for name, result := range current {
		results = append(results, buildInfoResult(dbPath, name, result))
	}

	// Compare with last week if available
	lastWeek := history.LastWeekSnapshot(currentWeek)
	if lastWeek == nil {
		return results
	}

	for name, currentResult := range current {
		prevResult, ok := lastWeek.Results[name]
		if !ok {
			continue
		}

		driftResults := compareSingleCheck(dbPath, name, currentResult, prevResult, lastWeek.Week)
		results = append(results, driftResults...)
	}

	return results
}

func buildInfoResult(dbPath, checkName string, result CheckResult) sarif.Result {
	var msg string
	if result.Breakdown != nil {
		parts := make([]string, 0, len(result.Breakdown))
		for k, v := range result.Breakdown {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
		msg = fmt.Sprintf("[%s] %s", checkName, strings.Join(parts, ", "))
	} else {
		msg = fmt.Sprintf("[%s] %d", checkName, result.Scalar)
	}

	return sarif.Result{
		RuleID: "db-check-info",
		Level:  "note",
		Message: sarif.Message{
			Text: msg,
		},
		Locations: []sarif.Location{
			{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
				},
			},
		},
	}
}

func compareSingleCheck(dbPath, checkName string, current, previous CheckResult, prevWeek string) []sarif.Result {
	var results []sarif.Result

	if current.Breakdown != nil && previous.Breakdown != nil {
		for key, currVal := range current.Breakdown {
			prevVal := previous.Breakdown[key]
			if currVal != prevVal {
				delta := currVal - prevVal
				sign := "+"
				if delta < 0 {
					sign = ""
				}
				msg := fmt.Sprintf("[%s] %s: %d → %d (%s%d since %s)", checkName, key, prevVal, currVal, sign, delta, prevWeek)
				results = append(results, sarif.Result{
					RuleID: "db-check-drift",
					Level:  "warning",
					Message: sarif.Message{
						Text: msg,
					},
					Locations: []sarif.Location{
						{
							PhysicalLocation: sarif.PhysicalLocation{
								ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
							},
						},
					},
				})
			}
		}

		// Check for keys that existed before but don't now
		for key, prevVal := range previous.Breakdown {
			if _, ok := current.Breakdown[key]; !ok {
				msg := fmt.Sprintf("[%s] %s: %d → 0 (removed since %s)", checkName, key, prevVal, prevWeek)
				results = append(results, sarif.Result{
					RuleID: "db-check-drift",
					Level:  "warning",
					Message: sarif.Message{
						Text: msg,
					},
					Locations: []sarif.Location{
						{
							PhysicalLocation: sarif.PhysicalLocation{
								ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
							},
						},
					},
				})
			}
		}
	} else if current.Scalar != previous.Scalar {
		delta := current.Scalar - previous.Scalar
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		msg := fmt.Sprintf("[%s] %d → %d (%s%d since %s)", checkName, previous.Scalar, current.Scalar, sign, delta, prevWeek)
		results = append(results, sarif.Result{
			RuleID: "db-check-drift",
			Level:  "warning",
			Message: sarif.Message{
				Text: msg,
			},
			Locations: []sarif.Location{
				{
					PhysicalLocation: sarif.PhysicalLocation{
						ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
					},
				},
			},
		})
	}

	return results
}
