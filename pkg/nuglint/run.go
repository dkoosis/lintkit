package nuglint

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Run executes nuglint across the provided paths.
func Run(paths []string) ([]sarif.Result, error) {
	files, err := collectFiles(paths)
	if err != nil {
		return nil, err
	}

	var results []sarif.Result
	for _, file := range files {
		fileResults, err := lintFile(file)
		if err != nil {
			return nil, err
		}
		results = append(results, fileResults...)
	}

	return results, nil
}

func collectFiles(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				if filepath.Ext(path) == ".jsonl" {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		} else {
			if filepath.Ext(p) == ".jsonl" {
				files = append(files, p)
			}
		}
	}
	return files, nil
}

func lintFile(path string) ([]sarif.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var results []sarif.Result
	reader := bufio.NewReader(f)
	lineNum := 0
	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		lineNum++
		line = strings.TrimSpace(line)
		if line == "" {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		var raw map[string]any
		if jsonErr := json.Unmarshal([]byte(line), &raw); jsonErr != nil {
			results = append(results, result("nug-json-parse", "error", fmt.Sprintf("failed to parse JSON: %v", jsonErr), path, lineNum))
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		nug := mapToNug(raw)
		validations := validateNug(nug)
		for _, v := range validations {
			v.Locations = []sarif.Location{location(path, lineNum)}
			results = append(results, v)
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	return results, nil
}

func mapToNug(raw map[string]any) Nug {
	nug := Nug{}
	if id, ok := raw["id"].(string); ok {
		nug.ID = id
	}
	if kind, ok := raw["k"].(string); ok {
		nug.Kind = kind
	}
	if r, ok := raw["r"].(string); ok {
		nug.Rationale = r
	}
	if tags, ok := raw["tags"].([]any); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				nug.Tags = append(nug.Tags, s)
			}
		}
	}
	_, nug.HasSev = raw["sev"]
	return nug
}

func validateNug(nug Nug) []sarif.Result {
	var results []sarif.Result

	if nug.ID == "" || nug.Kind == "" || nug.Rationale == "" {
		results = append(results, result("nug-required-fields", "error", "nugget must include id, k, and r", "", 0))
	}

	if msg := checkIDFormat(nug); msg != "" {
		results = append(results, result("nug-id-format", "error", msg, "", 0))
	}

	rationaleMap, rationaleErr := parseRationale(nug.Rationale)
	if rationaleErr != nil {
		results = append(results, result("nug-rationale-yaml", "error", rationaleErr.Error(), "", 0))
	}

	if rationaleErr == nil {
		if msg := checkKindStructure(nug.Kind, rationaleMap); msg != "" {
			results = append(results, result("nug-kind-structure", "error", msg, "", 0))
		}
	}

	if strings.ToLower(nug.Kind) == "trap" && !nug.HasSev {
		results = append(results, result("nug-severity-required", "error", "trap nuggets must include sev", "", 0))
	}

	if len(nug.Tags) == 0 {
		results = append(results, result("nug-orphan", "warning", "nugget should include tags or anchors", "", 0))
	}

	return results
}

func parseRationale(r string) (map[string]any, error) {
	scanner := bufio.NewScanner(strings.NewReader(r))
	result := map[string]any{}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "-") {
			return nil, fmt.Errorf("rationale YAML must be a mapping")
		}
		parts := strings.SplitN(text, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("rationale is not valid YAML: line %d is not a mapping entry", line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("rationale is not valid YAML: line %d has empty key", line)
		}
		result[normalizeKey(key)] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("rationale is not valid YAML: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("rationale YAML must be a mapping")
	}
	return result, nil
}

func checkIDFormat(n Nug) string {
	if n.ID == "" || n.Kind == "" {
		return ""
	}
	slugPattern := regexp.MustCompile(`^[a-z0-9-]+$`)
	parts := strings.Split(n.ID, ":")
	if len(parts) != 3 || parts[0] != "n" {
		return "id must follow n:{kind}:{slug}"
	}
	if parts[1] != n.Kind {
		return fmt.Sprintf("id kind '%s' does not match k '%s'", parts[1], n.Kind)
	}
	if !slugPattern.MatchString(parts[2]) {
		return "id slug must be lowercase letters, numbers, or hyphens"
	}
	return ""
}

func checkKindStructure(kind string, rationale map[string]any) string {
	normalizedKind := strings.ToLower(kind)
	fieldSet := map[string]struct{}{}
	for k := range rationale {
		fieldSet[normalizeKey(k)] = struct{}{}
	}

	requireAll := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := fieldSet[k]; !ok {
				return false
			}
		}
		return true
	}

	requireAny := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := fieldSet[k]; ok {
				return true
			}
		}
		return false
	}

	switch normalizedKind {
	case "trap":
		if !requireAll("problem", "symptoms") || !requireAny("workaround", "fix") {
			return "trap rationale should include problem, symptoms, and a workaround/fix"
		}
	case "choice":
		if !requireAll("decision", "context", "options", "rationale") {
			return "choice rationale should include decision, context, options, and rationale"
		}
	case "map":
		if !requireAll("pattern", "relationships") || !requireAny("flow", "components") {
			return "map rationale should include pattern, relationships, and flow/components"
		}
	case "cite":
		if !requireAll("title", "author", "keypoints") {
			return "cite rationale should include title, author, and key_points"
		}
		if !requireAny("url", "doi", "arxiv", "isbn") {
			return "cite rationale should include at least one locator (url/doi/arxiv/isbn)"
		}
	case "spark":
		if !requireAll("idea", "hypothesis", "validationapproach") {
			return "spark rationale should include idea, hypothesis, and validation_approach"
		}
	case "check":
		if !requireAll("invariant", "violationconsequence", "enforcement") {
			return "check rationale should include invariant, violation_consequence, and enforcement"
		}
	case "rule":
		if !requireAll("rulestatement", "doexamples", "dontexamples") {
			return "rule rationale should include rule_statement, do_examples, and dont_examples"
		}
	}

	return ""
}

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func result(ruleID, level, msg, path string, line int) sarif.Result {
	res := sarif.Result{
		RuleID:  ruleID,
		Level:   level,
		Message: sarif.Message{Text: msg},
	}
	if path != "" && line > 0 {
		res.Locations = []sarif.Location{location(path, line)}
	}
	return res
}

func location(path string, line int) sarif.Location {
	return sarif.Location{PhysicalLocation: sarif.PhysicalLocation{
		ArtifactLocation: sarif.ArtifactLocation{URI: path},
		Region:           &sarif.Region{StartLine: line},
	}}
}
