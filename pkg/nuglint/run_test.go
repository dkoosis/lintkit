package nuglint

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempJSONL(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "nuglint-*.jsonl")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}
	return f.Name()
}

func TestValidTrap(t *testing.T) {
	content := `{"id":"n:trap:leaky-bucket","k":"trap","r":"problem: leak\nsymptoms: water on floor\nworkaround: use seal","tags":["ops"],"sev":2}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{path})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no findings, got %d", len(results))
	}
}

func TestIDMismatch(t *testing.T) {
	content := `{"id":"n:choice:bad","k":"trap","r":"problem: leak\nsymptoms: drip\nworkaround: tape","tags":["ops"],"sev":1}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{path})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	found := false
	for _, r := range results {
		if r.RuleID == "nug-id-format" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected id format violation")
	}
}

func TestRationaleStructure(t *testing.T) {
	content := `{"id":"n:trap:missing-structure","k":"trap","r":"not yaml!","tags":[],"sev":1}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{path})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	hasYAMLErr := false
	for _, r := range results {
		if r.RuleID == "nug-rationale-yaml" {
			hasYAMLErr = true
		}
	}
	if !hasYAMLErr {
		t.Fatalf("expected rationale yaml violation")
	}
}

func TestKindStructure(t *testing.T) {
	content := `{"id":"n:choice:incomplete","k":"choice","r":"decision: pick db","tags":["arch"]}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{path})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	has := false
	for _, r := range results {
		if r.RuleID == "nug-kind-structure" {
			has = true
		}
	}
	if !has {
		t.Fatalf("expected kind structure violation")
	}
}

func TestSeverityRequired(t *testing.T) {
	content := `{"id":"n:trap:no-sev","k":"trap","r":"problem: leak\nsymptoms: drip\nfix: tape","tags":["ops"]}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{path})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	has := false
	for _, r := range results {
		if r.RuleID == "nug-severity-required" {
			has = true
		}
	}
	if !has {
		t.Fatalf("expected severity violation")
	}
}

func TestOrphan(t *testing.T) {
	content := `{"id":"n:check:orphan","k":"check","r":"invariant: always\nviolation_consequence: bad\nenforcement: unit tests"}`
	path := writeTempJSONL(t, content+"\n")

	results, err := Run([]string{filepath.Dir(path)})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	has := false
	for _, r := range results {
		if r.RuleID == "nug-orphan" {
			has = true
		}
	}
	if !has {
		t.Fatalf("expected orphan warning")
	}
}
