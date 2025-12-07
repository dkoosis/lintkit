package jsonl

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Validator wraps a compiled JSON Schema for validating documents.
type Validator struct {
	schema *schemaDefinition
}

// NewValidator compiles the JSON Schema at the provided path.
func NewValidator(schemaPath string) (*Validator, error) {
	schema, err := compileSchema(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}

	return &Validator{schema: schema}, nil
}

// ValidateFile validates a JSONL file line by line and returns SARIF results for failures.
func ValidateFile(path string, validator *Validator) ([]sarif.Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var results []sarif.Result
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Text()
		if strings.TrimSpace(raw) == "" {
			continue
		}

		var value interface{}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			results = append(results, newResult(path, line, fmt.Sprintf("line %d: invalid JSON: %v", line, err)))
			continue
		}

		if err := validator.schema.validate(value); err != nil {
			results = append(results, newResult(path, line, fmt.Sprintf("line %d: %v", line, err)))
		}
	}

	if err := scanner.Err(); err != nil {
		return results, err
	}

	return results, nil
}

func newResult(path string, line int, message string) sarif.Result {
	return sarif.Result{
		RuleID: "jsonl-schema",
		Level:  "error",
		Message: sarif.Message{
			Text: message,
		},
		Locations: []sarif.Location{
			{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{
						URI: filepath.ToSlash(path),
					},
					Region: &sarif.Region{StartLine: line},
				},
			},
		},
	}
}
