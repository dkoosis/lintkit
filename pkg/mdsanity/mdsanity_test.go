package mdsanity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

func TestRunDetectsOrphansAndLinks(t *testing.T) {
	dir := t.TempDir()

	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatalf("create dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	write("README.md", "[Guide](docs/guide.md)")
	write("docs/guide.md", "[Details](details.md)")
	write("docs/details.md", "")
	write("draft-plan.md", "")
	write("docs/notes/wip-idea.md", "")

	log, err := Run(Config{RepoRoot: dir})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	results := log.Runs[0].Results
	if len(results) == 0 {
		t.Fatalf("expected findings")
	}

	hasRule := func(ruleID string) bool {
		for _, r := range results {
			if r.RuleID == ruleID {
				return true
			}
		}
		return false
	}

	if !hasRule("md-orphan") {
		t.Errorf("expected orphan finding for draft-plan.md")
	}
	if !hasRule("md-root-clutter") {
		t.Errorf("expected root clutter finding for draft-plan.md")
	}
	if !hasRule("md-ephemeral-placement") {
		t.Errorf("expected ephemeral placement finding for draft-plan.md")
	}
	if hasRule("md-ephemeral-placement") && hasRule("md-root-clutter") && !hasRule("md-orphan") {
		t.Logf("results: %v", results)
	}

	if hasRuleFor(results, "docs/guide.md", "md-orphan") {
		t.Errorf("guide.md should be reachable")
	}
	if hasRuleFor(results, "docs/details.md", "md-orphan") {
		t.Errorf("details.md should be reachable")
	}

	if hasRuleFor(results, "docs/notes/wip-idea.md", "md-ephemeral-placement") {
		t.Errorf("ephemeral content in notes subtree should be allowed")
	}
}

func hasRuleFor(results []sarif.Result, path, rule string) bool {
	for _, r := range results {
		if r.RuleID != rule {
			continue
		}
		for _, loc := range r.Locations {
			if loc.PhysicalLocation.ArtifactLocation.URI == path {
				return true
			}
		}
	}
	return false
}
