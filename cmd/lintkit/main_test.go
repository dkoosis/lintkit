package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "data.json")
	if err := os.WriteFile(filePath, bytes.Repeat([]byte{'a'}, 1500), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	rulesContent := []byte("rules:\n  - pattern: '*.json'\n    max: 1KB\n")
	rulesFile, err := os.CreateTemp(tempDir, "rules-*.yml")
	if err != nil {
		t.Fatalf("temp rules: %v", err)
	}
	if _, err := rulesFile.Write(rulesContent); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	rulesFile.Close()

	var out bytes.Buffer
	if err := run([]string{"--rules", rulesFile.Name(), tempDir}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}

	var log struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.NewDecoder(&out).Decode(&log); err != nil {
		t.Fatalf("decode sarif: %v", err)
	}

	if len(log.Runs) != 1 || len(log.Runs[0].Results) == 0 {
		t.Fatalf("expected results in sarif output")
	}
	if log.Runs[0].Results[0].RuleID != "filesize-budget" {
		t.Fatalf("expected budget finding, got %s", log.Runs[0].Results[0].RuleID)
	}
}
