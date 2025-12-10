package sarif_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dkoosis/lintkit/pkg/sarif"
)

// failingWriter simulates a writer that fails after first write attempt.
type failingWriter struct{}

func (f failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failure")
}

func TestNewLog_ReturnsInitializedLog_When_Created(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "success: default log fields are populated"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := sarif.NewLog()

			if log.Version != sarif.Version {
				t.Fatalf("version mismatch: got %s", log.Version)
			}
			if log.Schema != "https://json.schemastore.org/sarif-2.1.0.json" {
				t.Fatalf("schema mismatch: got %s", log.Schema)
			}
			if log.Runs == nil {
				t.Fatalf("runs slice should be initialized")
			}
			if len(log.Runs) != 0 {
				t.Fatalf("runs slice should start empty, got %d", len(log.Runs))
			}
		})
	}
}

func TestEncoder_HandlesEncodingScenarios_When_WritingLogs(t *testing.T) {
	t.Parallel()

	simpleLog := &sarif.Log{
		Version: sarif.Version,
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Runs: []sarif.Run{{
			Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit"}},
			Results: []sarif.Result{{
				RuleID:  "RL001",
				Level:   "warning",
				Message: sarif.Message{Text: "something happened"},
				Locations: []sarif.Location{{
					PhysicalLocation: sarif.PhysicalLocation{
						ArtifactLocation: sarif.ArtifactLocation{URI: "file.go"},
						Region:           &sarif.Region{StartLine: 10, StartColumn: 2},
					},
				}},
			}},
		}},
	}

	tests := []struct {
		name    string
		writer  func() (io.Writer, *bytes.Buffer)
		log     *sarif.Log
		wantErr string
		inspect func(t *testing.T, buf *bytes.Buffer)
	}{
		{
			name: "error: writer failure is returned",
			writer: func() (io.Writer, *bytes.Buffer) {
				return failingWriter{}, nil
			},
			log:     simpleLog,
			wantErr: "write failure",
		},
		{
			name: "success: log is encoded with indentation",
			writer: func() (io.Writer, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				return buf, buf
			},
			log: simpleLog,
			inspect: func(t *testing.T, buf *bytes.Buffer) {
				output := buf.String()
				if !strings.Contains(output, "\n  \"version\"") {
					t.Fatalf("expected indented output, got %s", output)
				}
				if !strings.Contains(output, "\"ruleId\": \"RL001\"") {
					t.Fatalf("expected rule id in output, got %s", output)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			writer, buf := tc.writer()
			enc := sarif.NewEncoder(writer)

			err := enc.Encode(tc.log)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.inspect != nil {
				tc.inspect(t, buf)
			}
		})
	}
}
