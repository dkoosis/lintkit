package dbsanity

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// CheckResult holds the result of a single check execution.
type CheckResult struct {
	// Scalar holds the result for scalar checks.
	Scalar int64 `json:"scalar,omitempty"`
	// Breakdown holds the result for breakdown checks.
	Breakdown map[string]int64 `json:"breakdown,omitempty"`
}

// Snapshot captures all check results at a point in time.
type Snapshot struct {
	Timestamp time.Time              `json:"timestamp"`
	Week      string                 `json:"week"`
	Results   map[string]CheckResult `json:"results"`
}

// History stores historical check results.
type History struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// LoadHistory reads history from a JSON file. Returns empty history if file doesn't exist.
func LoadHistory(path string) (History, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return History{Snapshots: []Snapshot{}}, nil
		}
		return History{}, err
	}

	var h History
	if err := json.Unmarshal(data, &h); err != nil {
		return History{}, err
	}

	return h, nil
}

// SaveHistory writes history to a JSON file.
func SaveHistory(path string, h History) error {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// AddSnapshot appends a new snapshot to history.
func (h *History) AddSnapshot(s Snapshot) {
	h.Snapshots = append(h.Snapshots, s)
}

// LastSnapshot returns the most recent snapshot, or nil if empty.
func (h *History) LastSnapshot() *Snapshot {
	if len(h.Snapshots) == 0 {
		return nil
	}
	return &h.Snapshots[len(h.Snapshots)-1]
}

// LastWeekSnapshot returns the most recent snapshot from a different week than current.
func (h *History) LastWeekSnapshot(currentWeek string) *Snapshot {
	for i := len(h.Snapshots) - 1; i >= 0; i-- {
		if h.Snapshots[i].Week != currentWeek {
			return &h.Snapshots[i]
		}
	}
	return nil
}

// ISOWeek returns the ISO week string (e.g., "2025-W49") for a given time.
func ISOWeek(t time.Time) string {
	year, week := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
