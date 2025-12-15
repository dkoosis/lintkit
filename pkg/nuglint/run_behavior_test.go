package nuglint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dkoosis/lintkit/pkg/nuglint"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

func TestRun_ProducesExpectedFindings_When_GivenDiverseInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		files         map[string]string
		paths         []string
		expectErr     bool
		assertResults func(t *testing.T, results []sarif.Result, path string)
	}{
		{
			name:      "error: missing input path returns error",
			paths:     []string{"does-not-exist.jsonl"},
			expectErr: true,
		},
		{
			name: "error: invalid JSON reports parse failure with location",
			files: map[string]string{
				"input.jsonl": `not-json
{"id":"n:trap:valid","k":"trap","r":"problem: leak\nsymptoms: drip\nworkaround: patch","tags":["ops"],"sev":1}
`,
			},
			assertResults: func(t *testing.T, results []sarif.Result, path string) {
				require.Len(t, results, 1)
				assert.Equal(t, "nug-json-parse", results[0].RuleID)
				if assert.Len(t, results[0].Locations, 1) {
					loc := results[0].Locations[0].PhysicalLocation
					assert.Equal(t, filepath.ToSlash(path), loc.ArtifactLocation.URI)
					assert.Equal(t, 1, loc.Region.StartLine)
				}
				assert.Contains(t, results[0].Message.Text, "failed to parse JSON")
			},
		},
		{
			name: "warning: trap without severity is flagged",
			files: map[string]string{
				"trap.jsonl": `{"id":"n:trap:missing-sev","k":"trap","r":"problem: leak\nsymptoms: drip\nworkaround: tape","tags":["ops"]}
`,
			},
			assertResults: func(t *testing.T, results []sarif.Result, path string) {
				require.Len(t, results, 1)
				assert.Equal(t, "nug-severity-required", results[0].RuleID)
				if assert.Len(t, results[0].Locations, 1) {
					loc := results[0].Locations[0].PhysicalLocation
					assert.Equal(t, filepath.ToSlash(path), loc.ArtifactLocation.URI)
					assert.Equal(t, 1, loc.Region.StartLine)
				}
				assert.Contains(t, results[0].Message.Text, "trap nuggets must include sev")
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			var targetPath string
			for name, content := range tc.files {
				p := filepath.Join(tempDir, name)
				require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
				// Use first file as primary path for assertions.
				if targetPath == "" {
					targetPath = p
				}
			}

			paths := tc.paths
			if len(paths) == 0 {
				for name := range tc.files {
					paths = append(paths, filepath.Join(tempDir, name))
				}
			}

			results, err := nuglint.Run(paths)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tc.assertResults != nil {
				tc.assertResults(t, results, targetPath)
			}
		})
	}
}
