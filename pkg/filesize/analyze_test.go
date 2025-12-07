package filesize

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateRuleBytes(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "big.bin")
	content := bytes.Repeat([]byte{'x'}, 2048)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	rule := Rule{Pattern: "*.bin", MaxBytes: ptrInt64(1024)}
	metrics, err := collectMetrics([]string{tempDir}, false)
	if err != nil {
		t.Fatalf("collectMetrics: %v", err)
	}

	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}

	over, res := evaluateRule(rule, metrics[0])
	if !over {
		t.Fatalf("expected rule to be exceeded")
	}
	if res.RuleID != ruleIDBudget {
		t.Fatalf("unexpected rule id %s", res.RuleID)
	}
}

func TestAnalyzeEmitsSarif(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "small.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	analyzer := NewAnalyzer(nil)
	log, err := analyzer.Analyze([]string{path})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if len(log.Runs) != 1 {
		t.Fatalf("expected one run, got %d", len(log.Runs))
	}
	if len(log.Runs[0].Results) == 0 {
		t.Fatalf("expected metrics result when no rules provided")
	}
}

func ptrInt64(v int64) *int64 { return &v }
