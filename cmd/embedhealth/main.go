// Command embedhealth checks embedding health in the knowledge graph.
//
// Detects pathological conditions that silently break semantic search:
//   - Mixed embedding dimensions (cosine similarity returns 0 for mismatches)
//   - Low embedding coverage (nugs invisible to semantic/hybrid search)
//   - Missing embedder configuration
//
// Usage:
//
//	embedhealth                      # default human output
//	embedhealth -format=check        # lintkit-check JSON
//	embedhealth -db=/path/to.db      # custom database path
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/dkoosis/lintkit/pkg/check"
	_ "github.com/mattn/go-sqlite3"
)

type dimCount struct {
	Dims  int
	Count int
}

type kindCoverage struct {
	Kind     string
	Total    int
	Embedded int
}

type stats struct {
	Total        int
	Embedded     int
	Dimensions   []dimCount
	KindCoverage []kindCoverage
}

func main() {
	dbPath := flag.String("db", "", "path to knowledge.db (default: ~/.orca/knowledge.db)")
	format := flag.String("format", "human", "output format: check, human")
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	s, err := gather(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	report := buildReport(s)
	switch *format {
	case "check":
		if err := report.Write(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		cfg := check.DefaultHumanConfig()
		cfg.Purpose = "Embedding health for semantic search"
		report.WriteHuman(os.Stdout, cfg)
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
	return sql.Open("sqlite3", "file:"+path+"?mode=ro")
}

func gather(db *sql.DB) (*stats, error) {
	s := &stats{}

	// Check if nugs table exists with embedding column
	var colExists bool
	err := db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('nugs') WHERE name = 'embedding'`).Scan(&colExists)
	if err != nil || !colExists {
		return s, nil
	}

	// Total and embedded counts
	err = db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN embedding IS NOT NULL AND length(embedding) > 0 THEN 1 ELSE 0 END) FROM nugs`).
		Scan(&s.Total, &s.Embedded)
	if err != nil {
		return nil, fmt.Errorf("count nugs: %w", err)
	}

	// Dimension distribution
	rows, err := db.Query(`SELECT length(embedding)/4 as dims, COUNT(*) FROM nugs WHERE embedding IS NOT NULL AND length(embedding) > 0 GROUP BY dims ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, fmt.Errorf("query dimensions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var dc dimCount
		if err := rows.Scan(&dc.Dims, &dc.Count); err != nil {
			return nil, fmt.Errorf("scan dims: %w", err)
		}
		s.Dimensions = append(s.Dimensions, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dims: %w", err)
	}

	// Per-kind coverage
	rows, err = db.Query(`SELECT k, COUNT(*) as total, SUM(CASE WHEN embedding IS NOT NULL AND length(embedding) > 0 THEN 1 ELSE 0 END) as embedded FROM nugs GROUP BY k ORDER BY total DESC`)
	if err != nil {
		return nil, fmt.Errorf("query kind coverage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kc kindCoverage
		if err := rows.Scan(&kc.Kind, &kc.Total, &kc.Embedded); err != nil {
			return nil, fmt.Errorf("scan kind: %w", err)
		}
		s.KindCoverage = append(s.KindCoverage, kc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kinds: %w", err)
	}

	return s, nil
}

func buildReport(s *stats) *check.Report {
	report := check.NewReport("embedhealth")

	if s.Total == 0 {
		report.Status = check.StatusWarn
		report.Summary = "No nuggets found"
		return report
	}

	coveragePct := 100.0 * float64(s.Embedded) / float64(s.Total)
	missing := s.Total - s.Embedded

	report.Metrics = []check.Metric{
		{Name: "total", Value: float64(s.Total), Unit: "nugs"},
		{Name: "embedded", Value: float64(s.Embedded), Unit: "nugs"},
		{Name: "coverage", Value: coveragePct, Unit: "%", Threshold: 90, Op: check.OpGTE},
		{Name: "missing", Value: float64(missing), Unit: "nugs"},
		{Name: "dimensions", Value: float64(len(s.Dimensions)), Unit: "distinct", Threshold: 1, Op: check.OpLTE},
	}

	// Dimension mismatch detection — the critical check
	if len(s.Dimensions) > 1 {
		// Sort by count descending to identify majority vs minority
		sorted := make([]dimCount, len(s.Dimensions))
		copy(sorted, s.Dimensions)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Count > sorted[j].Count })

		majorityDim := sorted[0].Dims
		var minorityCount int
		for _, dc := range sorted[1:] {
			minorityCount += dc.Count
		}

		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityError,
			Label:    "dimension-mismatch",
			Value:    fmt.Sprintf("%d distinct dimensions", len(s.Dimensions)),
			Message: fmt.Sprintf(
				"majority=%d-dim (%d nugs), mismatched=%d nugs — cosine similarity returns 0 for cross-dimension pairs",
				majorityDim, sorted[0].Count, minorityCount,
			),
			Fix: "Run 'orca kg backfill' with correct ORCA_EMBEDDER_PROVIDER to re-embed all nugs at uniform dimension",
		})

		for _, dc := range sorted {
			report.Items = append(report.Items, check.Item{
				Severity: check.SeverityInfo,
				Label:    fmt.Sprintf("%d-dim", dc.Dims),
				Value:    fmt.Sprintf("%d nugs", dc.Count),
				Message:  fmt.Sprintf("%.0f%% of embedded corpus", 100.0*float64(dc.Count)/float64(s.Embedded)),
			})
		}
	}

	// Coverage gaps by kind
	for _, kc := range s.KindCoverage {
		if kc.Total == 0 {
			continue
		}
		pct := 100.0 * float64(kc.Embedded) / float64(kc.Total)
		gap := kc.Total - kc.Embedded
		if gap == 0 {
			continue
		}
		sev := check.SeverityInfo
		if pct == 0 {
			sev = check.SeverityError
		} else if pct < 80 {
			sev = check.SeverityWarn
		}
		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    fmt.Sprintf("coverage/%s", kc.Kind),
			Value:    fmt.Sprintf("%.0f%% (%d/%d)", pct, kc.Embedded, kc.Total),
			Message:  fmt.Sprintf("%d nugs invisible to semantic search", gap),
		})
	}

	// Derive status from items
	report.Status = report.DeriveStatus()

	// Summary
	if len(s.Dimensions) > 1 {
		report.Summary = fmt.Sprintf(
			"MIXED DIMENSIONS: %d distinct (%d nugs cross-dim invisible) | coverage %.0f%% (%d missing)",
			len(s.Dimensions), s.Embedded-s.Dimensions[0].Count, coveragePct, missing,
		)
	} else if coveragePct < 90 {
		report.Summary = fmt.Sprintf("coverage %.0f%% (%d/%d embedded, %d missing)", coveragePct, s.Embedded, s.Total, missing)
	} else {
		dimStr := "no embeddings"
		if len(s.Dimensions) == 1 {
			dimStr = fmt.Sprintf("%d-dim", s.Dimensions[0].Dims)
		}
		report.Summary = fmt.Sprintf("healthy: %s, %.0f%% coverage (%d/%d)", dimStr, coveragePct, s.Embedded, s.Total)
	}

	return report
}
