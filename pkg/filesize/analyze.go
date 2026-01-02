// Package filesize checks file sizes against budget rules.
package filesize

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

const (
	ruleIDBudget  = "filesize-budget"
	ruleIDMetrics = "filesize-metrics"
)

// FileMetric describes a single file's measurements.
type FileMetric struct {
	Path      string
	SizeBytes int64
	Lines     *int
}

// Analyzer encapsulates rule evaluation and SARIF emission.
type Analyzer struct {
	rules []Rule
}

// NewAnalyzer creates an analyzer for the provided rules.
func NewAnalyzer(rules []Rule) *Analyzer {
	return &Analyzer{rules: rules}
}

// Analyze walks the provided paths (or "." if empty), evaluates rules, and
// returns a SARIF log with the findings.
func (a *Analyzer) Analyze(paths []string) (*sarif.Log, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}

	metrics, err := collectMetrics(paths, a.needsLineCounts())
	if err != nil {
		return nil, err
	}

	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Path < metrics[j].Path })

	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-filesize"}},
	}

	for _, m := range metrics {
		rule := a.matchRule(m.Path)
		if rule == nil {
			run.Results = append(run.Results, infoResult(m))
			continue
		}

		over, result := evaluateRule(*rule, m)
		if over {
			run.Results = append(run.Results, result)
		}
	}

	log := sarif.NewLog()
	log.Runs = append(log.Runs, run)
	return log, nil
}

func (a *Analyzer) matchRule(path string) *Rule {
	for i := range a.rules {
		if matchPath(path, a.rules[i].Pattern) {
			return &a.rules[i]
		}
	}
	return nil
}

func (a *Analyzer) needsLineCounts() bool {
	for _, r := range a.rules {
		if r.MaxLines != nil {
			return true
		}
	}
	return len(a.rules) == 0 // metrics mode should include line counts when possible
}

func collectMetrics(paths []string, includeLines bool) ([]FileMetric, error) {
	var metrics []FileMetric
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}

		if info.IsDir() {
			err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				metric, err := measureFile(path, wd, includeLines)
				if err != nil {
					return err
				}
				metrics = append(metrics, metric)
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}

		metric, err := measureFile(p, wd, includeLines)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	return metrics, nil
}

func measureFile(path, workdir string, includeLines bool) (FileMetric, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileMetric{}, fmt.Errorf("stat %s: %w", path, err)
	}

	rel, err := filepath.Rel(workdir, path)
	if err != nil {
		rel = path
	}

	metric := FileMetric{Path: filepath.ToSlash(rel), SizeBytes: info.Size()}

	if includeLines {
		lines, err := countLines(path)
		if err != nil {
			// If the file cannot be scanned as text, fall back to bytes only.
			var perr *textDecodingError
			if errors.As(err, &perr) {
				return metric, nil
			}
			return FileMetric{}, err
		}
		metric.Lines = &lines
	}

	return metric, nil
}

func evaluateRule(rule Rule, metric FileMetric) (bool, sarif.Result) {
	if rule.MaxBytes != nil && metric.SizeBytes > *rule.MaxBytes {
		return true, sarif.Result{
			RuleID:  ruleIDBudget,
			Level:   "warning",
			Message: sarif.Message{Text: fmt.Sprintf("%s exceeds max bytes: %d > %d", metric.Path, metric.SizeBytes, *rule.MaxBytes)},
			Locations: []sarif.Location{{
				PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: metric.Path}},
			}},
		}
	}

	if rule.MaxLines != nil && metric.Lines != nil && *metric.Lines > *rule.MaxLines {
		return true, sarif.Result{
			RuleID:  ruleIDBudget,
			Level:   "warning",
			Message: sarif.Message{Text: fmt.Sprintf("%s exceeds max lines: %d > %d", metric.Path, *metric.Lines, *rule.MaxLines)},
			Locations: []sarif.Location{{
				PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: metric.Path}},
			}},
		}
	}

	return false, sarif.Result{}
}

func infoResult(metric FileMetric) sarif.Result {
	message := fmt.Sprintf("%s: %d bytes", metric.Path, metric.SizeBytes)
	if metric.Lines != nil {
		message = fmt.Sprintf("%s (%d bytes, %d lines)", metric.Path, metric.SizeBytes, *metric.Lines)
	}

	return sarif.Result{
		RuleID:  ruleIDMetrics,
		Level:   "note",
		Message: sarif.Message{Text: message},
		Locations: []sarif.Location{{
			PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: metric.Path}},
		}},
	}
}

func matchPath(path, pattern string) bool {
	path = filepath.ToSlash(path)
	pattern = filepath.ToSlash(pattern)
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	if matched {
		return true
	}

	// Allow matching just the base name for simple patterns like "*.go".
	base := filepath.Base(path)
	matched, _ = filepath.Match(pattern, base)
	return matched
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)
	const bufSize = 64 * 1024
	buf := make([]byte, bufSize)
	lines := 0
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return 0, &textDecodingError{path: path}
			}
			lines += strings.Count(string(chunk), "\n")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	return lines, nil
}

// textDecodingError is returned when a file is likely binary and cannot be read
// as text.
type textDecodingError struct {
	path string
}

func (e *textDecodingError) Error() string {
	return fmt.Sprintf("cannot decode %s as text", e.path)
}
