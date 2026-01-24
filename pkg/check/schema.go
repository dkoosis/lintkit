// Package check provides types and helpers for the lintkit-check output format.
//
// lintkit-check is a standard format for reporting metrics, thresholds, and
// non-location-based findings from linters. It complements SARIF (which handles
// location-based violations) for cases like file size budgets, coverage metrics,
// and health checks.
package check

// Report is the top-level structure for lintkit-check output.
type Report struct {
	Schema  string   `json:"$schema"`           // Always "lintkit-check"
	Tool    string   `json:"tool"`              // Linter name (e.g., "filesize")
	Status  Status   `json:"status"`            // Overall status: pass, warn, fail
	Summary string   `json:"summary"`           // One-line human description
	Metrics []Metric `json:"metrics,omitempty"` // Numeric measurements
	Items   []Item   `json:"items,omitempty"`   // Ranked list of findings
	Trend   []int    `json:"trend,omitempty"`   // Historical values for sparkline
}

// Status represents the overall check result.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Metric represents a numeric measurement with optional threshold.
type Metric struct {
	Name      string  `json:"name"`                // Metric identifier (e.g., "red_files")
	Value     float64 `json:"value"`               // Current value
	Threshold float64 `json:"threshold,omitempty"` // Threshold for pass/fail
	Op        Op      `json:"op,omitempty"`        // Comparison operator (default: lte)
	Unit      string  `json:"unit,omitempty"`      // Unit (e.g., "LOC", "%", "ms")
}

// Op is a comparison operator for threshold evaluation.
type Op string

const (
	OpLT  Op = "lt"  // Less than
	OpLTE Op = "lte" // Less than or equal (default)
	OpGT  Op = "gt"  // Greater than
	OpGTE Op = "gte" // Greater than or equal
	OpEQ  Op = "eq"  // Equal
)

// Passes returns true if the metric passes its threshold check.
func (m Metric) Passes() bool {
	if m.Threshold == 0 && m.Op == "" {
		return true // No threshold defined
	}
	op := m.Op
	if op == "" {
		op = OpLTE
	}
	switch op {
	case OpLT:
		return m.Value < m.Threshold
	case OpLTE:
		return m.Value <= m.Threshold
	case OpGT:
		return m.Value > m.Threshold
	case OpGTE:
		return m.Value >= m.Threshold
	case OpEQ:
		return m.Value == m.Threshold
	default:
		return m.Value <= m.Threshold
	}
}

// Item represents a single finding in a ranked list.
type Item struct {
	Severity Severity `json:"severity"`          // error, warn, info
	Label    string   `json:"label"`             // Display name (e.g., "server.go")
	Value    string   `json:"value,omitempty"`   // Measurement (e.g., "1847 LOC")
	Path     string   `json:"path,omitempty"`    // File path for linking
	Message  string   `json:"message,omitempty"` // Detail message
	Fix      string   `json:"fix,omitempty"`     // Suggested fix
}

// Severity indicates the importance of an item.
type Severity string

const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

// DeriveStatus computes status from metrics and items if not explicitly set.
func (r *Report) DeriveStatus() Status {
	// Check metrics
	for _, m := range r.Metrics {
		if m.Threshold != 0 && !m.Passes() {
			return StatusFail
		}
	}
	// Check items
	hasWarn := false
	for _, item := range r.Items {
		if item.Severity == SeverityError {
			return StatusFail
		}
		if item.Severity == SeverityWarn {
			hasWarn = true
		}
	}
	if hasWarn {
		return StatusWarn
	}
	return StatusPass
}
