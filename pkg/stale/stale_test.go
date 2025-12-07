package stale

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigDefaultsMode(t *testing.T) {
	rulesYAML := []byte(`
rules:
  - derived: "go.sum"
    source: "go.mod"
`)
	tmpFile := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(tmpFile, rulesYAML, 0o644); err != nil {
		t.Fatalf("write temp rules: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}

	if cfg.Rules[0].Mode != ModeMTime {
		t.Fatalf("expected default mode %q, got %q", ModeMTime, cfg.Rules[0].Mode)
	}
}

func TestEvaluateMTime(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Rules: []Rule{
			{Derived: "output.bin", Source: "schema.txt"},
		},
	}

	derivedPath := filepath.Join(dir, "output.bin")
	sourcePath := filepath.Join(dir, "schema.txt")

	if err := os.WriteFile(derivedPath, []byte("derived"), 0o644); err != nil {
		t.Fatalf("write derived: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("source"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(derivedPath, older, older); err != nil {
		t.Fatalf("set derived time: %v", err)
	}
	if err := os.Chtimes(sourcePath, newer, newer); err != nil {
		t.Fatalf("set source time: %v", err)
	}

	results, err := Evaluate(dir, cfg)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].RuleID != ruleID {
		t.Errorf("unexpected rule ID: %s", results[0].RuleID)
	}

	if results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI != "output.bin" {
		t.Errorf("expected derived relative path, got %s", results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI)
	}
}

func TestEvaluateNotStale(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Rules: []Rule{{Derived: "output.bin", Source: "schema.txt"}}}

	derivedPath := filepath.Join(dir, "output.bin")
	sourcePath := filepath.Join(dir, "schema.txt")

	if err := os.WriteFile(derivedPath, []byte("derived"), 0o644); err != nil {
		t.Fatalf("write derived: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("source"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sourcePath, newer, newer); err != nil {
		t.Fatalf("set source time: %v", err)
	}
	if err := os.Chtimes(derivedPath, older, older); err != nil {
		t.Fatalf("set derived time: %v", err)
	}

	results, err := Evaluate(dir, cfg)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}
