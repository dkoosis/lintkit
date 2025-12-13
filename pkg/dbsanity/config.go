package dbsanity

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// CheckType defines the kind of result a check produces.
type CheckType string

const (
	// CheckTypeScalar returns a single numeric value.
	CheckTypeScalar CheckType = "scalar"
	// CheckTypeBreakdown returns a key-value mapping of counts.
	CheckTypeBreakdown CheckType = "breakdown"
)

// Check defines a single data quality check.
type Check struct {
	Name  string    `yaml:"name"`
	Query string    `yaml:"query"`
	Type  CheckType `yaml:"type"`
}

// Config holds the configuration for data checks.
type Config struct {
	Checks []Check `yaml:"checks"`
}

// LoadConfig reads a YAML configuration file for data checks.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	return parseChecksConfig(string(data))
}

// parseChecksConfig parses YAML config without external dependencies.
func parseChecksConfig(content string) (Config, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var cfg Config
	var current *Check

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if trimmed == "checks:" {
			continue
		}

		if strings.HasPrefix(trimmed, "-") {
			if current != nil {
				cfg.Checks = append(cfg.Checks, *current)
			}
			current = &Check{}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
			if err := populateCheckField(current, trimmed); err != nil {
				return Config{}, err
			}
			continue
		}

		if current != nil {
			if err := populateCheckField(current, trimmed); err != nil {
				return Config{}, err
			}
		}
	}

	if current != nil {
		cfg.Checks = append(cfg.Checks, *current)
	}

	if err := scanner.Err(); err != nil {
		return Config{}, err
	}

	// Validate and set defaults
	for i := range cfg.Checks {
		if cfg.Checks[i].Name == "" {
			return Config{}, fmt.Errorf("check at index %d missing name", i)
		}
		if cfg.Checks[i].Query == "" {
			return Config{}, fmt.Errorf("check %q missing query", cfg.Checks[i].Name)
		}
		if cfg.Checks[i].Type == "" {
			cfg.Checks[i].Type = CheckTypeScalar
		}
	}

	return cfg, nil
}

func populateCheckField(check *Check, line string) error {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid check line: %s", line)
	}

	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])
	val = strings.Trim(val, "\"")

	switch key {
	case "name":
		check.Name = val
	case "query":
		check.Query = val
	case "type":
		check.Type = CheckType(val)
	}

	return nil
}
