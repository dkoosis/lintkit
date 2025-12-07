package dbschema

import "github.com/dkoosis/lintkit/pkg/sarif"

// ToSARIF converts findings to SARIF results for a specific database artifact.
func ToSARIF(dbPath string, findings []Result) []sarif.Result {
	results := make([]sarif.Result, 0, len(findings))
	for _, f := range findings {
		results = append(results, sarif.Result{
			RuleID: f.RuleID,
			Level:  f.Level,
			Message: sarif.Message{
				Text: f.Text,
			},
			Locations: []sarif.Location{{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: dbPath},
					Region:           &sarif.Region{StartLine: 1},
				},
			}},
		})
	}
	return results
}
