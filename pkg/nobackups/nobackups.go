// Package nobackups detects backup and temporary files in the codebase.
package nobackups

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Scan walks the provided paths (or the current directory if none are given)
// and reports backup or temporary files as SARIF results.
func Scan(paths []string) (*sarif.Log, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}

	walker := &scanner{
		patterns: defaultPatterns(),
	}

	for _, root := range paths {
		if err := walker.walk(root); err != nil {
			return nil, err
		}
	}

	sort.Slice(walker.results, func(i, j int) bool {
		return walker.results[i].Locations[0].PhysicalLocation.ArtifactLocation.URI <
			walker.results[j].Locations[0].PhysicalLocation.ArtifactLocation.URI
	})

	log := sarif.NewLog()
	log.Runs = []sarif.Run{{
		Tool:    sarif.Tool{Driver: sarif.Driver{Name: "lintkit-nobackups"}},
		Results: walker.results,
	}}

	return log, nil
}

type scanner struct {
	patterns []pattern
	results  []sarif.Result
}

func (s *scanner) walk(root string) error {
	if root == "" {
		return errors.New("empty path provided")
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if s.isBackup(d.Name()) {
			s.results = append(s.results, sarif.Result{
				RuleID: "nobackups",
				Level:  "warning",
				Message: sarif.Message{
					Text: fmt.Sprintf("Backup/temporary file should not be committed: %s", path),
				},
				Locations: []sarif.Location{{
					PhysicalLocation: sarif.PhysicalLocation{
						ArtifactLocation: sarif.ArtifactLocation{URI: filepath.ToSlash(path)},
					},
				}},
			})
		}

		return nil
	})
}

func (s *scanner) isBackup(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range s.patterns {
		if p.matches(lower) {
			return true
		}
	}
	return false
}

type pattern struct {
	suffix string
	ext    string
}

func (p pattern) matches(name string) bool {
	if p.ext != "" && strings.HasSuffix(name, p.ext) {
		return true
	}
	if p.suffix != "" && strings.HasSuffix(name, p.suffix) {
		return true
	}
	return false
}

func defaultPatterns() []pattern {
	return []pattern{
		{ext: ".bak"},
		{ext: ".backup"},
		{ext: ".old"},
		{ext: ".orig"},
		{ext: ".swp"},
		{ext: ".swo"},
		{suffix: "~"},
	}
}
