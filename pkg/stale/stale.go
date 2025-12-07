package stale

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

const (
	ruleID       = "stale-artifact"
	defaultLevel = "warning"
)

// RuleMode describes how staleness is determined. Future modes may
// include hash or git-based comparisons. The initial implementation
// supports only mtime.
type RuleMode string

const (
	// ModeMTime checks modification times between derived and source files.
	ModeMTime RuleMode = "mtime"
)

// Rule describes the relationship between derived and source artifacts.
type Rule struct {
	Derived string   `yaml:"derived"`
	Source  string   `yaml:"source"`
	Mode    RuleMode `yaml:"mode,omitempty"`
}

// Config is the root of the YAML configuration file.
type Config struct {
	Rules []Rule `yaml:"rules"`
}

// LoadConfig reads a YAML configuration file from disk.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	cfg, err := parseConfig(data)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func parseConfig(data []byte) (Config, error) {
	rules, err := parseRulesYAML(string(data))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Rules: rules}
	for i := range cfg.Rules {
		if cfg.Rules[i].Mode == "" {
			cfg.Rules[i].Mode = ModeMTime
		}
	}

	return cfg, nil
}

func parseRulesYAML(content string) ([]Rule, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var rules []Rule
	var current *Rule

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if trimmed == "rules:" {
			continue
		}

		if strings.HasPrefix(trimmed, "-") {
			if current != nil {
				rules = append(rules, *current)
			}

			current = &Rule{}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if trimmed == "" {
				continue
			}
			if err := populateRuleField(current, trimmed); err != nil {
				return nil, err
			}
			continue
		}

		if current != nil {
			if err := populateRuleField(current, trimmed); err != nil {
				return nil, err
			}
		}
	}

	if current != nil {
		rules = append(rules, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return rules, nil
}

func populateRuleField(rule *Rule, line string) error {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid rule line: %s", line)
	}

	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])
	val = strings.Trim(val, "\"")

	switch key {
	case "derived":
		rule.Derived = val
	case "source":
		rule.Source = val
	case "mode":
		rule.Mode = RuleMode(val)
	}

	return nil
}

// Evaluate executes the configured rules against the provided root
// directory, returning SARIF results for any derived artifacts that are
// older than their sources.
func Evaluate(root string, cfg Config) ([]sarif.Result, error) {
	var results []sarif.Result

	for _, rule := range cfg.Rules {
		mode := rule.Mode
		if mode == "" {
			mode = ModeMTime
		}

		switch mode {
		case ModeMTime:
			ruleResults, err := evaluateMTimeRule(root, rule)
			if err != nil {
				return nil, err
			}
			results = append(results, ruleResults...)
		default:
			return nil, fmt.Errorf("unsupported rule mode: %s", rule.Mode)
		}
	}

	return results, nil
}

func evaluateMTimeRule(root string, rule Rule) ([]sarif.Result, error) {
	derivedPattern := filepath.Join(root, rule.Derived)
	derivedPaths, err := filepath.Glob(derivedPattern)
	if err != nil {
		return nil, fmt.Errorf("glob derived pattern %q: %w", rule.Derived, err)
	}

	sourcePattern := filepath.Join(root, rule.Source)
	sourcePaths, err := filepath.Glob(sourcePattern)
	if err != nil {
		return nil, fmt.Errorf("glob source pattern %q: %w", rule.Source, err)
	}

	// No source files to compare; nothing to mark stale.
	if len(sourcePaths) == 0 {
		return nil, nil
	}

	var results []sarif.Result
	for _, derived := range derivedPaths {
		derivedInfo, err := os.Stat(derived)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat derived file %q: %w", derived, err)
		}

		derivedMTime := derivedInfo.ModTime()
		for _, source := range sourcePaths {
			sourceInfo, err := os.Stat(source)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("stat source file %q: %w", source, err)
			}

			if isSourceNewer(sourceInfo.ModTime(), derivedMTime) {
				results = append(results, buildResult(root, derived, source))
				break
			}
		}
	}

	return results, nil
}

func isSourceNewer(sourceTime, derivedTime time.Time) bool {
	return sourceTime.After(derivedTime)
}

func buildResult(root, derived, source string) sarif.Result {
	derivedRel, err := filepath.Rel(root, derived)
	if err != nil {
		derivedRel = derived
	}

	sourceRel, err := filepath.Rel(root, source)
	if err != nil {
		sourceRel = source
	}

	message := fmt.Sprintf("derived file %s is older than source %s", derivedRel, sourceRel)

	return sarif.Result{
		RuleID: ruleID,
		Level:  defaultLevel,
		Message: sarif.Message{
			Text: message,
		},
		Locations: []sarif.Location{
			{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: derivedRel},
				},
			},
		},
	}
}
