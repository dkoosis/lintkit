package docsprawl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

func TestReadmeTooLarge(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "README.md"), "one\ntwo\n")

	res, err := Run([]string{tmp}, Config{MaxReadmeLines: 1, MaxFilesPerDir: 10, DuplicateCutoff: 0.9})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !hasRule(res.Log, "doc-readme-too-large") {
		t.Fatalf("expected README size warning")
	}
}

func TestDirectoryFileCount(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "a.md"), "a")
	writeFile(t, filepath.Join(tmp, "b.md"), "b")
	writeFile(t, filepath.Join(tmp, "c.md"), "c")

	res, err := Run([]string{tmp}, Config{MaxReadmeLines: 50, MaxFilesPerDir: 2, DuplicateCutoff: 0.9})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !hasRule(res.Log, "doc-too-many-files") {
		t.Fatalf("expected directory file count warning")
	}
}

func TestOrphanDetection(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "README.md"), "[Doc](docs/a.md)")
	if err := os.MkdirAll(filepath.Join(tmp, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	writeFile(t, filepath.Join(tmp, "docs", "a.md"), "linked")
	writeFile(t, filepath.Join(tmp, "docs", "b.md"), "orphaned")

	res, err := Run([]string{tmp}, Config{MaxReadmeLines: 50, MaxFilesPerDir: 10, DuplicateCutoff: 0.9})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !hasRule(res.Log, "doc-orphan") {
		t.Fatalf("expected orphan warning")
	}
}

func TestDuplicateDetection(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "README.md"), "[One](one.md) [Two](two.md)")
	writeFile(t, filepath.Join(tmp, "one.md"), "Shared content across docs with only minor changes.")
	writeFile(t, filepath.Join(tmp, "two.md"), "Shared content across docs with only minor change.")

	res, err := Run([]string{tmp}, Config{MaxReadmeLines: 50, MaxFilesPerDir: 10, DuplicateCutoff: 0.6})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !hasRule(res.Log, "doc-duplicate") {
		t.Fatalf("expected duplicate warning")
	}
}

func hasRule(log *sarif.Log, rule string) bool {
	for _, run := range log.Runs {
		for _, r := range run.Results {
			if r.RuleID == rule {
				return true
			}
		}
	}
	return false
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if strings.HasSuffix(path, string(os.PathSeparator)) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}
