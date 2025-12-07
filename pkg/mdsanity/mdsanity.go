package mdsanity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Config controls the analysis behavior.
type Config struct {
	// RepoRoot is the path to the repository root.
	RepoRoot string
	// EntryPoints are optional markdown files that represent starting points for reachability.
	// If none are provided, README.md in the repo root is used when present.
	EntryPoints []string
}

// Run executes the markdown hygiene analysis and returns a SARIF log.
func Run(cfg Config) (*sarif.Log, error) {
	root, err := filepath.Abs(cfg.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	mdFiles, err := collectMarkdownFiles(root)
	if err != nil {
		return nil, err
	}

	entryPoints := cfg.EntryPoints
	if len(entryPoints) == 0 {
		if _, ok := mdFiles["README.md"]; ok {
			entryPoints = []string{"README.md"}
		}
	}
	if len(entryPoints) == 0 {
		return nil, errors.New("no entry points found; provide README.md or configure entry points")
	}

	graph, err := buildLinkGraph(root, mdFiles)
	if err != nil {
		return nil, err
	}

	reachable := findReachable(entryPoints, graph)

	results := []sarif.Result{}
	for rel, abs := range mdFiles {
		if _, ok := reachable[rel]; !ok {
			results = append(results, makeResult("md-orphan", fmt.Sprintf("%s is not reachable from any entry point", rel), rel))
		}

		if isRootClutter(rel) {
			results = append(results, makeResult("md-root-clutter", fmt.Sprintf("%s lives at the repository root; move it under docs/ or another documentation subtree", rel), rel))
		}

		if isEphemeral(rel) && !inEphemeralSubtree(rel) {
			results = append(results, makeResult("md-ephemeral-placement", fmt.Sprintf("%s looks ephemeral but is not stored in a dedicated drafts/notes area", rel), rel))
		}

		_ = abs // currently unused but available for future checks
	}

	log := sarif.NewLog()
	log.Runs = append(log.Runs, sarif.Run{
		Tool:    sarif.Tool{Driver: sarif.Driver{Name: "mdsanity"}},
		Results: results,
	})

	return log, nil
}

func makeResult(ruleID, text, relPath string) sarif.Result {
	return sarif.Result{
		RuleID:    ruleID,
		Level:     "warning",
		Message:   sarif.Message{Text: text},
		Locations: []sarif.Location{locationFor(relPath)},
	}
}

func locationFor(relPath string) sarif.Location {
	return sarif.Location{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: relPath}}}
}

func collectMarkdownFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	skipDirs := map[string]struct{}{".git": {}, "node_modules": {}, "vendor": {}, ".idea": {}, ".vscode": {}}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		// normalize to forward slashes for SARIF
		rel = filepath.ToSlash(rel)
		files[rel] = path
		return nil
	})

	return files, err
}

func buildLinkGraph(root string, files map[string]string) (map[string][]string, error) {
	linkGraph := make(map[string][]string)
	for rel, abs := range files {
		links, err := extractLinks(abs, rel, root)
		if err != nil {
			return nil, err
		}
		filtered := make([]string, 0, len(links))
		for _, target := range links {
			if _, exists := files[target]; exists {
				filtered = append(filtered, target)
			}
		}
		linkGraph[rel] = filtered
	}
	return linkGraph, nil
}

var linkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

func extractLinks(absPath, relPath, root string) ([]string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	matches := linkPattern.FindAllStringSubmatch(string(data), -1)
	results := make([]string, 0, len(matches))

	for _, match := range matches {
		target := match[1]
		if target == "" || strings.HasPrefix(target, "#") {
			continue
		}
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}

		if idx := strings.Index(target, "#"); idx != -1 {
			target = target[:idx]
		}
		if target == "" {
			continue
		}

		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		}

		resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.Join(root, relPath)), target))
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)

		if strings.EqualFold(filepath.Ext(rel), ".md") {
			results = append(results, rel)
			continue
		}

		// Allow implicit README when linking to a directory.
		if !strings.Contains(filepath.Base(target), ".") {
			candidate := filepath.ToSlash(filepath.Join(rel, "README.md"))
			results = append(results, candidate)
		}
	}

	return results, nil
}

func findReachable(entryPoints []string, graph map[string][]string) map[string]struct{} {
	reachable := make(map[string]struct{})
	queue := append([]string{}, entryPoints...)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if _, seen := reachable[current]; seen {
			continue
		}
		reachable[current] = struct{}{}

		for _, next := range graph[current] {
			if _, seen := reachable[next]; !seen {
				queue = append(queue, next)
			}
		}
	}

	return reachable
}

func isRootClutter(rel string) bool {
	if strings.Contains(rel, "/") {
		return false
	}
	lower := strings.ToLower(rel)
	switch lower {
	case "readme.md", "license.md", "contributing.md":
		return false
	default:
		return true
	}
}

func isEphemeral(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	patterns := []string{"draft", "wip", "tmp", "scratch", "notes"}
	for _, p := range patterns {
		if strings.Contains(base, p) {
			return true
		}
	}
	return false
}

func inEphemeralSubtree(rel string) bool {
	segments := strings.Split(strings.ToLower(filepath.Dir(rel)), "/")
	allowed := map[string]struct{}{"draft": {}, "drafts": {}, "wip": {}, "tmp": {}, "scratch": {}, "notes": {}, "adr": {}, "adrs": {}}
	for _, seg := range segments {
		if _, ok := allowed[seg]; ok {
			return true
		}
	}
	return false
}

// PrettyPrint is a helper for debugging.
func PrettyPrint(log *sarif.Log) string {
	data, _ := json.MarshalIndent(log, "", "  ")
	return string(data)
}
