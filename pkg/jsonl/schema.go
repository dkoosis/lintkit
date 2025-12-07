package jsonl

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// schemaDefinition represents a limited subset of JSON Schema used for validation.
type schemaDefinition struct {
	Type                 string                       `json:"type"`
	Required             []string                     `json:"required"`
	Properties           map[string]*schemaDefinition `json:"properties"`
	AdditionalProperties *bool                        `json:"additionalProperties"`
	Items                *schemaDefinition            `json:"items"`
}

// compileSchema reads and parses a JSON Schema document.
func compileSchema(path string) (*schemaDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)

	var schema schemaDefinition
	if err := dec.Decode(&schema); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	return &schema, nil
}

func (s *schemaDefinition) validate(value interface{}) error {
	switch s.Type {
	case "object", "":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected object")
		}

		required := map[string]struct{}{}
		for _, r := range s.Required {
			required[r] = struct{}{}
		}

		for key, val := range obj {
			delete(required, key)
			if propSchema, ok := s.Properties[key]; ok && propSchema != nil {
				if err := propSchema.validate(val); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
			} else if s.AdditionalProperties != nil && !*s.AdditionalProperties {
				return fmt.Errorf("unexpected property %q", key)
			}
		}

		if len(required) > 0 {
			missing := make([]string, 0, len(required))
			for key := range required {
				missing = append(missing, key)
			}
			return fmt.Errorf("missing required properties: %s", strings.Join(missing, ", "))
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string")
		}
		return nil
	case "integer":
		switch v := value.(type) {
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("expected integer")
			}
		case int, int32, int64, uint, uint32, uint64:
			// already integer
		default:
			return fmt.Errorf("expected integer")
		}
		return nil
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("expected number")
		}
		return nil
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
		return nil
	case "array":
		arr, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("expected array")
		}
		if s.Items != nil {
			for i, item := range arr {
				if err := s.Items.validate(item); err != nil {
					return fmt.Errorf("index %d: %w", i, err)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported schema type %q", s.Type)
	}
}
