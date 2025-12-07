package jsonl

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

func TestValidateFileValid(t *testing.T) {
	schema := filepath.Join("testdata", "simple.schema.json")
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	results, err := ValidateFile(filepath.Join("testdata", "valid.jsonl"), validator)
	if err != nil {
		t.Fatalf("validate file: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestValidateFileInvalid(t *testing.T) {
	schema := filepath.Join("testdata", "simple.schema.json")
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	results, err := ValidateFile(filepath.Join("testdata", "invalid.jsonl"), validator)
	if err != nil {
		t.Fatalf("validate file: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].RuleID != "jsonl-schema" || results[0].Level != "error" {
		t.Fatalf("unexpected rule or level: %+v", results[0])
	}

	if results[0].Locations[0].PhysicalLocation.Region.StartLine != 2 {
		t.Fatalf("expected first error on line 2, got %d", results[0].Locations[0].PhysicalLocation.Region.StartLine)
	}

	if results[1].Locations[0].PhysicalLocation.Region.StartLine != 3 {
		t.Fatalf("expected second error on line 3, got %d", results[1].Locations[0].PhysicalLocation.Region.StartLine)
	}

	if results[2].Locations[0].PhysicalLocation.Region.StartLine != 4 {
		t.Fatalf("expected third error on line 4, got %d", results[2].Locations[0].PhysicalLocation.Region.StartLine)
	}
}

func TestNewValidatorInvalidSchema(t *testing.T) {
	schema := filepath.Join("testdata", "invalid.schema.json")
	if _, err := NewValidator(schema); err == nil {
		t.Fatalf("expected error for invalid schema")
	}
}

func TestSarifEncoding(t *testing.T) {
	result := newResult("file.jsonl", 5, "line 5: example")

	log := sarif.NewLog()
	log.Runs = append(log.Runs, sarif.Run{Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-jsonl"}}, Results: []sarif.Result{result}})

	data, err := json.Marshal(log)
	if err != nil {
		t.Fatalf("encode log: %v", err)
	}

	if !strings.Contains(string(data), "lintkit-jsonl") {
		t.Fatalf("expected driver name in encoded sarif")
	}
}
