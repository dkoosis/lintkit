// Package wikifmt checks wiki-style markdown files for formatting issues.
package wikifmt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Run executes the wikifmt linter against the provided root directories.
func Run(roots []string) (*sarif.Log, error) {
	files, err := collectFiles(roots)
	if err != nil {
		return nil, err
	}

	index := buildIndex(files)

	var results []sarif.Result

	for _, f := range files {
		fmResults := checkFrontmatter(f)
		results = append(results, fmResults...)
	}

	// Tag hygiene checks need aggregated view.
	tagResults := checkTags(files)
	results = append(results, tagResults...)

	for _, f := range files {
		linkResults := checkLinks(f, index)
		results = append(results, linkResults...)
	}

	log := sarif.NewLog()
	log.Runs = append(log.Runs, sarif.Run{
		Tool:    sarif.Tool{Driver: sarif.Driver{Name: "lintkit-wikifmt"}},
		Results: results,
	})

	return log, nil
}

// wikiFile represents a parsed wiki markdown file.
type wikiFile struct {
	Path           string
	Content        string
	Lines          []string
	Frontmatter    frontmatter
	FrontmatterErr error
	Links          []link
	Tags           []tagEntry
}

type frontmatter struct {
	Title valueNode[string]
	Date  valueNode[string]
	Tags  valueNode[[]string]
}

type valueNode[T any] struct {
	Value T
	Line  int
	IsSet bool
}

type link struct {
	Target string
	Kind   string // wikilink or markdown
	Line   int
	Path   string // resolved path candidate
}

type tagEntry struct {
	Value string
	Line  int
}

var (
	frontmatterPattern    = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n?`)
	wikilinkPattern       = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	mdlinkPattern         = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	datePattern           = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	errMissingFrontmatter = errors.New("missing frontmatter")
)

func collectFiles(roots []string) ([]wikiFile, error) {
	var files []wikiFile
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				wf, err := parseFile(path)
				if err != nil {
					return err
				}
				files = append(files, wf)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func parseFile(path string) (wikiFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return wikiFile{}, err
	}
	content := string(data)
	lines := strings.Split(content, "\n")

	fm, fmErr := parseFrontmatter(content)
	links := parseLinks(lines)

	var tags []tagEntry
	if fm.Tags.IsSet {
		for _, t := range fm.Tags.Value {
			tags = append(tags, tagEntry{Value: t, Line: fm.Tags.Line})
		}
	}

	return wikiFile{
		Path:           path,
		Content:        content,
		Lines:          lines,
		Frontmatter:    fm,
		FrontmatterErr: fmErr,
		Links:          links,
		Tags:           tags,
	}, nil
}

func parseFrontmatter(content string) (frontmatter, error) {
	match := frontmatterPattern.FindStringSubmatch(content)
	if len(match) < 2 {
		return frontmatter{}, errMissingFrontmatter
	}

	fm := frontmatter{}
	raw := match[1]
	lines := strings.Split(raw, "\n")
	seenKeys := make(map[string]int)
	var currentKey string

	for i, line := range lines {
		lineNo := i + 2 // account for leading '---' line
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(line, "  - ") {
			return fm, fmt.Errorf("invalid YAML list entry without key on line %d", lineNo)
		}

		if strings.HasPrefix(line, "  - ") {
			if currentKey != "tags" {
				return fm, fmt.Errorf("unexpected list item on line %d", lineNo)
			}
			tag := strings.TrimSpace(strings.TrimPrefix(line, "  - "))
			fm.Tags.Value = append(fm.Tags.Value, tag)
			fm.Tags.Line = lineNo
			fm.Tags.IsSet = true
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return fm, fmt.Errorf("invalid frontmatter line %d", lineNo)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		currentKey = key

		if prev, ok := seenKeys[key]; ok {
			return fm, fmt.Errorf("duplicate key %q on line %d (previously on line %d)", key, lineNo, prev)
		}
		seenKeys[key] = lineNo

		switch key {
		case "title":
			if value != "" {
				fm.Title = valueNode[string]{Value: value, Line: lineNo, IsSet: true}
			}
		case "date":
			if value != "" {
				fm.Date = valueNode[string]{Value: value, Line: lineNo, IsSet: true}
			}
		case "tags":
			fm.Tags.Line = lineNo
			if value != "" {
				fm.Tags.Value = append(fm.Tags.Value, value)
				fm.Tags.IsSet = true
			}
		}
	}

	return fm, nil
}

func parseLinks(lines []string) []link {
	var links []link
	for i, line := range lines {
		lineNo := i + 1
		for _, m := range wikilinkPattern.FindAllStringSubmatch(line, -1) {
			links = append(links, link{Target: m[1], Kind: "wikilink", Line: lineNo})
		}
		for _, m := range mdlinkPattern.FindAllStringSubmatch(line, -1) {
			links = append(links, link{Target: m[1], Kind: "markdown", Line: lineNo})
		}
	}
	return links
}

func buildIndex(files []wikiFile) map[string]string {
	index := make(map[string]string)
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
		index[strings.ToLower(base)] = f.Path
		slug := slugify(base)
		index[slug] = f.Path
	}
	return index
}

func slugify(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	replacer := strings.NewReplacer(" ", "-", "_", "-")
	s = replacer.Replace(s)
	// remove duplicate dashes
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	return s
}

func checkFrontmatter(f wikiFile) []sarif.Result {
	var results []sarif.Result

	if f.FrontmatterErr != nil {
		results = append(results, newResult("wiki-frontmatter-yaml", "invalid frontmatter YAML: "+f.FrontmatterErr.Error(), f.Path, 1))
		if !errors.Is(f.FrontmatterErr, errMissingFrontmatter) {
			return results
		}
	}

	if !f.Frontmatter.Title.IsSet {
		results = append(results, newResult("wiki-frontmatter-required", "missing required frontmatter key: title", f.Path, 1))
	}
	if !f.Frontmatter.Date.IsSet {
		results = append(results, newResult("wiki-frontmatter-required", "missing required frontmatter key: date", f.Path, 1))
	} else if !datePattern.MatchString(f.Frontmatter.Date.Value) {
		results = append(results, newResult("wiki-date-format", fmt.Sprintf("date must be YYYY-MM-DD, got %q", f.Frontmatter.Date.Value), f.Path, f.Frontmatter.Date.Line))
	}
	if !f.Frontmatter.Tags.IsSet {
		results = append(results, newResult("wiki-frontmatter-required", "missing required frontmatter key: tags", f.Path, 1))
	}

	return results
}

func checkLinks(f wikiFile, index map[string]string) []sarif.Result {
	var results []sarif.Result
	for _, l := range f.Links {
		switch l.Kind {
		case "wikilink":
			if !resolveWikiLink(l.Target, index) {
				msg := fmt.Sprintf("broken wikilink [[%s]]", l.Target)
				results = append(results, newResult("wiki-link-broken", msg, f.Path, l.Line))
			}
		case "markdown":
			if !resolveMarkdownLink(f.Path, l.Target, index) {
				msg := fmt.Sprintf("broken markdown link %s", l.Target)
				results = append(results, newResult("wiki-link-broken", msg, f.Path, l.Line))
			}
		}
	}
	return results
}

func resolveWikiLink(target string, index map[string]string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	slug := slugify(target)
	if _, ok := index[slug]; ok {
		return true
	}
	lower := strings.ToLower(target)
	_, ok := index[lower]
	return ok
}

func resolveMarkdownLink(currentPath, target string, index map[string]string) bool {
	if target == "" {
		return false
	}
	cleaned := strings.TrimPrefix(target, "/")
	// resolve relative to current file directory
	dir := filepath.Dir(currentPath)
	abs := filepath.Clean(filepath.Join(dir, cleaned))
	key := strings.ToLower(strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
	slug := slugify(strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
	if _, ok := index[key]; ok {
		return true
	}
	if _, ok := index[slug]; ok {
		return true
	}
	// If link includes extension, check directly
	if strings.HasSuffix(abs, ".md") {
		for _, path := range index {
			if sameFile(abs, path) {
				return true
			}
		}
	}
	return false
}

func sameFile(a, b string) bool {
	ap, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bp, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	return ap == bp
}

func checkTags(files []wikiFile) []sarif.Result {
	type occurrence struct {
		file string
		line int
		raw  string
	}
	tagOccurrences := make(map[string][]occurrence)
	casing := make(map[string]map[string]struct{})

	for _, f := range files {
		for _, t := range f.Tags {
			norm := strings.ToLower(t.Value)
			tagOccurrences[norm] = append(tagOccurrences[norm], occurrence{file: f.Path, line: t.Line, raw: t.Value})
			if _, ok := casing[norm]; !ok {
				casing[norm] = make(map[string]struct{})
			}
			casing[norm][t.Value] = struct{}{}
		}
	}

	var results []sarif.Result

	for norm, occs := range tagOccurrences {
		if len(occs) == 1 {
			occ := occs[0]
			msg := fmt.Sprintf("tag %q is only used once", occ.raw)
			results = append(results, newResult("wiki-tag-orphan", msg, occ.file, occ.line))
		}
		if len(casing[norm]) > 1 {
			for _, occ := range occs {
				msg := fmt.Sprintf("tag %q has case variants; prefer consistent casing for %q", occ.raw, norm)
				results = append(results, newResult("wiki-tag-case-variant", msg, occ.file, occ.line))
			}
		}
	}

	return results
}

func newResult(ruleID, msg, path string, line int) sarif.Result {
	loc := sarif.Location{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: path}}}
	if line > 0 {
		loc.PhysicalLocation.Region = &sarif.Region{StartLine: line}
	}
	return sarif.Result{
		RuleID:    ruleID,
		Level:     levelForRule(ruleID),
		Message:   sarif.Message{Text: msg},
		Locations: []sarif.Location{loc},
	}
}

func levelForRule(rule string) string {
	switch rule {
	case "wiki-frontmatter-yaml", "wiki-frontmatter-required", "wiki-date-format", "wiki-link-broken":
		return "error"
	case "wiki-tag-case-variant", "wiki-tag-orphan":
		return "warning"
	default:
		return "note"
	}
}
