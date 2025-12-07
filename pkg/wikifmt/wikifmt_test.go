package wikifmt

import (
	"testing"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

func TestRunProducesSarif(t *testing.T) {
	log, err := Run([]string{"testdata/wiki"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(log.Runs))
	}

	results := log.Runs[0].Results
	assertHasResult(t, results, "wiki-frontmatter-yaml", "testdata/wiki/bad-yaml.md")
	assertHasResult(t, results, "wiki-frontmatter-required", "testdata/wiki/missing-tags.md")
	assertHasResult(t, results, "wiki-date-format", "testdata/wiki/bad-date.md")
	assertHasResult(t, results, "wiki-link-broken", "testdata/wiki/broken-links.md")
	assertHasResult(t, results, "wiki-tag-orphan", "testdata/wiki/orphan.md")

	// Case variant should flag both files using API/api.
	countCaseVariant := countRuleForPath(results, "wiki-tag-case-variant", "testdata/wiki/good.md") +
		countRuleForPath(results, "wiki-tag-case-variant", "testdata/wiki/api-guide.md")
	if countCaseVariant < 2 {
		t.Fatalf("expected case variant warnings for both files, got %d", countCaseVariant)
	}
}

func assertHasResult(t *testing.T, results []sarif.Result, rule, path string) {
	t.Helper()
	for _, r := range results {
		if r.RuleID == rule {
			for _, loc := range r.Locations {
				if loc.PhysicalLocation.ArtifactLocation.URI == path {
					return
				}
			}
		}
	}
	t.Fatalf("expected result %s for %s", rule, path)
}

func countRuleForPath(results []sarif.Result, rule, path string) int {
	count := 0
	for _, r := range results {
		if r.RuleID != rule {
			continue
		}
		for _, loc := range r.Locations {
			if loc.PhysicalLocation.ArtifactLocation.URI == path {
				count++
			}
		}
	}
	return count
}
