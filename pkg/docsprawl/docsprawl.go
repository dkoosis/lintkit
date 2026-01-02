// Package docsprawl analyzes markdown sprawl and emits SARIF reports.
package docsprawl

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Config controls docsprawl checks.
type Config struct {
	MaxReadmeLines  int
	MaxFilesPerDir  int
	DuplicateCutoff float64
}

// Result encapsulates analysis output.
type Result struct {
	Log *sarif.Log
}

// Run executes the docsprawl checks for the provided roots.
func Run(roots []string, cfg Config) (*Result, error) {
	if cfg.MaxReadmeLines <= 0 {
		return nil, fmt.Errorf("max README lines must be > 0")
	}
	if cfg.MaxFilesPerDir <= 0 {
		return nil, fmt.Errorf("max files per dir must be > 0")
	}
	if cfg.DuplicateCutoff <= 0 || cfg.DuplicateCutoff > 1 {
		return nil, fmt.Errorf("duplicate cutoff must be in (0,1]")
	}
	docs, dirCounts, err := collectDocs(roots)
	if err != nil {
		return nil, err
	}

	results := []sarif.Result{}
	results = append(results, checkReadmeSize(docs, cfg.MaxReadmeLines)...)
	results = append(results, checkDirFileCounts(dirCounts, cfg.MaxFilesPerDir)...)
	results = append(results, checkOrphans(docs)...)
	results = append(results, checkDuplicates(docs, cfg.DuplicateCutoff)...)

	log := sarif.NewLog()
	log.Runs = append(log.Runs, sarif.Run{
		Tool:    sarif.Tool{Driver: sarif.Driver{Name: "lintkit-docsprawl"}},
		Results: results,
	})

	return &Result{Log: log}, nil
}

// Command creates a flag set for the docsprawl CLI command.
func Command() *flag.FlagSet {
	fs := flag.NewFlagSet("docsprawl", flag.ExitOnError)
	//nolint:errcheck // CLI usage output
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: lintkit docsprawl [--max-readme=N] [--max-files=N] ROOT...\n")
	}
	return fs
}

// RunCLI parses flags and executes the command, writing SARIF to w.
func RunCLI(fs *flag.FlagSet, args []string, w io.Writer) error {
	maxReadme := fs.Int("max-readme", 500, "maximum allowed README lines")
	maxFiles := fs.Int("max-files", 10, "maximum markdown files per directory")
	duplicateCutoff := fs.Float64("duplicate-cutoff", 0.9, "similarity threshold for near-duplicates (0-1]")
	if err := fs.Parse(args); err != nil {
		return err
	}
	roots := fs.Args()
	if len(roots) == 0 {
		return errors.New("at least one ROOT must be specified")
	}
	cfg := Config{MaxReadmeLines: *maxReadme, MaxFilesPerDir: *maxFiles, DuplicateCutoff: *duplicateCutoff}
	res, err := Run(roots, cfg)
	if err != nil {
		return err
	}
	return res.Encode(w)
}

// Encode writes the SARIF log to the writer as indented JSON.
func (r *Result) Encode(w io.Writer) error {
	return encodeSARIF(w, r.Log)
}

// encodeSARIF marshals the log with indentation.
func encodeSARIF(w io.Writer, log *sarif.Log) error {
	enc := sarif.NewEncoder(w)
	return enc.Encode(log)
}

// Doc captures metadata for a markdown document.
type Doc struct {
	Path        string
	Dir         string
	Lines       int
	Content     string
	IsReadme    bool
	LinkTargets []string
	Shingles    map[string]struct{}
	Root        string
}

func collectDocs(roots []string) (map[string]*Doc, map[string]int, error) {
	docs := map[string]*Doc{}
	dirCounts := map[string]int{}
	for _, root := range roots {
		root = filepath.Clean(root)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !isMarkdown(d.Name()) {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Count(string(content), "\n") + 1
			dir := filepath.Dir(path)
			dirCounts[dir]++
			linkTargets := parseLinks(string(content), dir)
			doc := &Doc{
				Path:        path,
				Dir:         dir,
				Lines:       lines,
				Content:     string(content),
				IsReadme:    strings.EqualFold(filepath.Base(path), "README.md"),
				LinkTargets: linkTargets,
				Shingles:    buildShingles(string(content)),
				Root:        root,
			}
			docs[path] = doc
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return docs, dirCounts, nil
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md"
}

var linkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
var wordPattern = regexp.MustCompile(`[^a-z0-9]+`)

func parseLinks(content string, baseDir string) []string {
	matches := linkPattern.FindAllStringSubmatch(content, -1)
	links := []string{}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		target := m[1]
		if strings.Contains(target, "://") {
			continue
		}
		if idx := strings.Index(target, "#"); idx >= 0 {
			target = target[:idx]
		}
		if target == "" {
			continue
		}
		clean := target
		if !filepath.IsAbs(target) {
			clean = filepath.Join(baseDir, target)
		}
		clean = filepath.Clean(clean)
		links = append(links, clean)
	}
	return links
}

func checkReadmeSize(docs map[string]*Doc, maxLines int) []sarif.Result {
	var results []sarif.Result
	for _, doc := range docs {
		if doc.IsReadme && doc.Lines > maxLines {
			results = append(results, sarif.Result{
				RuleID:    "doc-readme-too-large",
				Level:     "warning",
				Message:   sarif.Message{Text: fmt.Sprintf("README exceeds %d lines (%d)", maxLines, doc.Lines)},
				Locations: []sarif.Location{locationForFile(doc.Path, 1)},
			})
		}
	}
	return results
}

func checkDirFileCounts(dirCounts map[string]int, maxFiles int) []sarif.Result {
	var results []sarif.Result
	for dir, count := range dirCounts {
		if count > maxFiles {
			results = append(results, sarif.Result{
				RuleID:    "doc-too-many-files",
				Level:     "warning",
				Message:   sarif.Message{Text: fmt.Sprintf("directory %s has %d markdown files (max %d)", dir, count, maxFiles)},
				Locations: []sarif.Location{locationForFile(dir, 0)},
			})
		}
	}
	return results
}

func checkOrphans(docs map[string]*Doc) []sarif.Result {
	roots := findRootReadmes(docs)
	if len(roots) == 0 {
		return nil
	}
	reachable := map[string]struct{}{}
	queue := append([]string{}, roots...)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if _, seen := reachable[path]; seen {
			continue
		}
		reachable[path] = struct{}{}
		doc := docs[path]
		for _, target := range doc.LinkTargets {
			if _, ok := docs[target]; ok {
				queue = append(queue, target)
			}
		}
	}

	var results []sarif.Result
	for path := range docs {
		if _, ok := reachable[path]; !ok {
			results = append(results, sarif.Result{
				RuleID:    "doc-orphan",
				Level:     "note",
				Message:   sarif.Message{Text: fmt.Sprintf("document is not reachable from a root README: %s", filepath.Base(path))},
				Locations: []sarif.Location{locationForFile(path, 1)},
			})
		}
	}
	return results
}

func findRootReadmes(docs map[string]*Doc) []string {
	roots := []string{}
	for path, doc := range docs {
		if doc.IsReadme && filepath.Clean(doc.Dir) == filepath.Clean(doc.Root) {
			roots = append(roots, path)
		}
	}
	sort.Strings(roots)
	return roots
}

func checkDuplicates(docs map[string]*Doc, cutoff float64) []sarif.Result {
	var results []sarif.Result
	paths := make([]string, 0, len(docs))
	for path := range docs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			a := docs[paths[i]]
			b := docs[paths[j]]
			sim := similarity(a.Shingles, b.Shingles)
			if sim >= cutoff {
				results = append(results, sarif.Result{
					RuleID:  "doc-duplicate",
					Level:   "warning",
					Message: sarif.Message{Text: fmt.Sprintf("documents appear nearly duplicate (similarity %.2f)", sim)},
					Locations: []sarif.Location{
						locationForFile(a.Path, 1),
						locationForFile(b.Path, 1),
					},
				})
			}
		}
	}
	return results
}

func buildShingles(content string) map[string]struct{} {
	tokens := tokenize(content)
	const size = 5
	shingles := map[string]struct{}{}
	if len(tokens) == 0 {
		return shingles
	}
	if len(tokens) < size {
		hash := hashStrings(tokens)
		shingles[hash] = struct{}{}
		return shingles
	}
	for i := 0; i <= len(tokens)-size; i++ {
		shingle := tokens[i : i+size]
		hash := hashStrings(shingle)
		shingles[hash] = struct{}{}
	}
	return shingles
}

func tokenize(content string) []string {
	cleaned := strings.ToLower(content)
	cleaned = strings.ReplaceAll(cleaned, "\r", "")
	cleaned = wordPattern.ReplaceAllString(cleaned, " ")
	parts := strings.Fields(cleaned)
	return parts
}

func hashStrings(parts []string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, strings.Join(parts, " "))
	return hex.EncodeToString(h.Sum(nil))
}

func similarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var inter int
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}

func locationForFile(path string, line int) sarif.Location {
	loc := sarif.Location{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: path}}}
	if line > 0 {
		loc.PhysicalLocation.Region = &sarif.Region{StartLine: line}
	}
	return loc
}
