package filesize

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Rule describes a single filesize constraint.
type Rule struct {
	Pattern  string
	MaxBytes *int64
	MaxLines *int
}

// ruleSpec mirrors the on-disk structure.
type ruleSpec struct {
	Pattern string
	Max     interface{}
}

// LoadRules reads rules from the provided path. If the path is empty, an empty
// slice is returned.
func LoadRules(path string) ([]Rule, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rules: %w", err)
	}

	specs, err := parseRuleSpecs(string(data))
	if err != nil {
		return nil, err
	}

	var rules []Rule
	for i, spec := range specs {
		rule, err := parseRuleSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

func parseRuleSpec(spec ruleSpec) (Rule, error) {
	if strings.TrimSpace(spec.Pattern) == "" {
		return Rule{}, errors.New("pattern is required")
	}

	maxStr := stringifyMax(spec.Max)
	bytes, lines, err := parseMaxValue(maxStr)
	if err != nil {
		return Rule{}, err
	}

	return Rule{
		Pattern:  spec.Pattern,
		MaxBytes: bytes,
		MaxLines: lines,
	}, nil
}

func stringifyMax(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%.0f", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// parseMaxValue interprets the max value. If it includes a unit suffix, it is
// treated as bytes. A bare integer is treated as a line count.
func parseMaxValue(v string) (*int64, *int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil, errors.New("max is required")
	}

	if bytes, ok := parseByteString(v); ok {
		return &bytes, nil, nil
	}

	lines, err := parseInt(v)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid max value %q", v)
	}
	return nil, &lines, nil
}

func parseInt(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid integer %q", s)
	}
	return n, nil
}

// parseByteString parses values like "10KB" or "2MB". It returns (0, false) on
// non-byte inputs.
func parseByteString(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ToUpper(s))
	units := []struct {
		suffix string
		factor int64
	}{
		{"KB", 1024},
		{"MB", 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"B", 1},
	}

	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			value := strings.TrimSuffix(s, u.suffix)
			n, err := parseInt(value)
			if err != nil {
				return 0, false
			}
			return int64(n) * u.factor, true
		}
	}
	return 0, false
}

// parseRuleSpecs implements a small YAML subset parser for the rule file. The
// accepted structure mirrors the README example and common "rules:" lists.
func parseRuleSpecs(content string) ([]ruleSpec, error) {
	lines := strings.Split(content, "\n")
	var specs []ruleSpec
	var current *ruleSpec
	inRules := false

	flush := func() {
		if current != nil {
			specs = append(specs, *current)
			current = nil
		}
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if !inRules {
			if line == "rules:" {
				inRules = true
				continue
			}
			continue
		}

		if strings.HasPrefix(line, "-") {
			flush()
			current = &ruleSpec{}
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if line == "" {
				continue
			}
			key, val, ok := splitKeyValue(line)
			if !ok {
				return nil, fmt.Errorf("invalid rule line: %s", raw)
			}
			assignRuleField(current, key, val)
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("unexpected content outside rule item: %s", raw)
		}

		key, val, ok := splitKeyValue(line)
		if !ok {
			return nil, fmt.Errorf("invalid rule line: %s", raw)
		}
		assignRuleField(current, key, val)
	}

	flush()
	return specs, nil
}

func splitKeyValue(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])
	val = strings.Trim(val, "\"'")
	return key, val, true
}

func assignRuleField(spec *ruleSpec, key, val string) {
	switch key {
	case "pattern":
		spec.Pattern = val
	case "max":
		spec.Max = val
	}
}
