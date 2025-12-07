package nobackups

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDetectsBackupFiles(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		"keep.txt",
		"notes.txt~",
		"archive.bak",
		"draft.old",
		"merge.orig",
		filepath.Join("nested", "swap.swp"),
		filepath.Join("nested", "temp.swo"),
	}

	for _, f := range files {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("create dir: %v", err)
		}
		if err := os.WriteFile(full, []byte("data"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	log, err := Scan([]string{dir})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(log.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(log.Runs))
	}

	got := map[string]bool{}
	for _, r := range log.Runs[0].Results {
		if r.RuleID != "nobackups" {
			t.Fatalf("unexpected rule ID: %s", r.RuleID)
		}
		for _, loc := range r.Locations {
			got[filepath.Base(loc.PhysicalLocation.ArtifactLocation.URI)] = true
		}
	}

	expected := []string{"notes.txt~", "archive.bak", "draft.old", "merge.orig", "swap.swp", "temp.swo"}
	for _, name := range expected {
		if !got[name] {
			t.Fatalf("expected finding for %s", name)
		}
	}

	if got["keep.txt"] {
		t.Fatalf("did not expect finding for keep.txt")
	}
}

func TestScanSingleFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.backup")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	log, err := Scan([]string{target})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(log.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(log.Runs))
	}

	if len(log.Runs[0].Results) != 1 {
		t.Fatalf("expected one result, got %d", len(log.Runs[0].Results))
	}

	result := log.Runs[0].Results[0]
	if result.RuleID != "nobackups" {
		t.Fatalf("unexpected rule ID: %s", result.RuleID)
	}

	if len(result.Locations) != 1 {
		t.Fatalf("expected one location, got %d", len(result.Locations))
	}

	if !strings.HasSuffix(result.Locations[0].PhysicalLocation.ArtifactLocation.URI, "file.backup") {
		t.Fatalf("unexpected uri: %s", result.Locations[0].PhysicalLocation.ArtifactLocation.URI)
	}
}
