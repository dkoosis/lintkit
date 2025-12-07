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
