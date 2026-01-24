package check

import (
	"fmt"
	"io"
	"strings"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

// Sparkline characters (eighth blocks, low to high)
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// HumanConfig configures human-readable output.
type HumanConfig struct {
	Color   bool   // Enable ANSI colors
	Width   int    // Max width (0 = no limit)
	Purpose string // Optional description of what this check does
}

// DefaultHumanConfig returns sensible defaults.
func DefaultHumanConfig() HumanConfig {
	return HumanConfig{
		Color: true,
		Width: 72,
	}
}

// WriteHuman writes a human-readable report to the given writer.
func (r *Report) WriteHuman(w io.Writer, cfg HumanConfig) {
	// Header
	title := strings.ToUpper(r.Tool)
	if cfg.Color {
		fmt.Fprintf(w, "%s%s═══ %s ═══%s\n", colorBold, colorCyan, title, colorReset)
	} else {
		fmt.Fprintf(w, "═══ %s ═══\n", title)
	}

	// Purpose (if provided)
	if cfg.Purpose != "" {
		if cfg.Color {
			fmt.Fprintf(w, "%sPurpose:%s %s\n", colorDim, colorReset, cfg.Purpose)
		} else {
			fmt.Fprintf(w, "Purpose: %s\n", cfg.Purpose)
		}
	}
	fmt.Fprintln(w)

	// Items
	if len(r.Items) > 0 {
		for _, item := range r.Items {
			icon, color := severityIcon(item.Severity, cfg.Color)
			if cfg.Color {
				fmt.Fprintf(w, "  %s%s%s ", color, icon, colorReset)
			} else {
				fmt.Fprintf(w, "  %s ", icon)
			}

			// Label and value
			if item.Value != "" {
				fmt.Fprintf(w, "%-40s %s\n", item.Label, item.Value)
			} else {
				fmt.Fprintf(w, "%s\n", item.Label)
			}

			// Message (indented)
			if item.Message != "" {
				if cfg.Color {
					fmt.Fprintf(w, "    %s%s%s\n", colorDim, item.Message, colorReset)
				} else {
					fmt.Fprintf(w, "    %s\n", item.Message)
				}
			}

			// Fix suggestion (indented)
			if item.Fix != "" {
				if cfg.Color {
					fmt.Fprintf(w, "    %sFix:%s %s\n", colorCyan, colorReset, item.Fix)
				} else {
					fmt.Fprintf(w, "    Fix: %s\n", item.Fix)
				}
			}
		}
		fmt.Fprintln(w)
	}

	// Metrics summary line
	if len(r.Metrics) > 0 {
		var parts []string
		for _, m := range r.Metrics {
			if m.Unit != "" {
				parts = append(parts, fmt.Sprintf("%s: %.0f %s", m.Name, m.Value, m.Unit))
			} else {
				parts = append(parts, fmt.Sprintf("%s: %.0f", m.Name, m.Value))
			}
		}
		if cfg.Color {
			fmt.Fprintf(w, "%s%s%s\n", colorDim, strings.Join(parts, " │ "), colorReset)
		} else {
			fmt.Fprintf(w, "%s\n", strings.Join(parts, " | "))
		}
	}

	// Trend sparkline
	if len(r.Trend) > 0 {
		spark := Sparkline(r.Trend)
		if cfg.Color {
			fmt.Fprintf(w, "%sTrend:%s %s\n", colorDim, colorReset, spark)
		} else {
			fmt.Fprintf(w, "Trend: %s\n", spark)
		}
	}

	// Summary with status
	if r.Summary != "" {
		fmt.Fprintln(w)
		icon, color := statusIcon(r.Status, cfg.Color)
		if cfg.Color {
			fmt.Fprintf(w, "%s%s%s %s\n", color, icon, colorReset, r.Summary)
		} else {
			fmt.Fprintf(w, "%s %s\n", icon, r.Summary)
		}
	}
}

// Sparkline converts a slice of values to a sparkline string.
func Sparkline(values []int) string {
	if len(values) == 0 {
		return ""
	}

	// Find min/max
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}

	// Build sparkline
	var sb strings.Builder
	rng := max - min
	if rng == 0 {
		rng = 1 // Avoid division by zero
	}
	for _, v := range values {
		idx := (v - min) * (len(sparkChars) - 1) / rng
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		sb.WriteRune(sparkChars[idx])
	}
	return sb.String()
}

func severityIcon(sev Severity, color bool) (string, string) {
	switch sev {
	case SeverityError:
		return "✗", colorRed
	case SeverityWarn:
		return "!", colorYellow
	case SeverityInfo:
		return "·", colorCyan
	default:
		return "·", ""
	}
}

func statusIcon(status Status, color bool) (string, string) {
	switch status {
	case StatusPass:
		return "✓", colorGreen
	case StatusWarn:
		return "!", colorYellow
	case StatusFail:
		return "✗", colorRed
	default:
		return "?", ""
	}
}
