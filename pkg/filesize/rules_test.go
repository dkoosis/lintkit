package filesize

import (
	"os"
	"testing"
)

func TestLoadRules(t *testing.T) {
	content := []byte(`rules:
  - pattern: "*.go"
    max: 200
  - pattern: "*.json"
    max: 10KB
`)
	f, err := os.CreateTemp(t.TempDir(), "rules-*.yml")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write rules: %v", err)
	}
	f.Close()

	rules, err := LoadRules(f.Name())
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	if rules[0].MaxLines == nil || *rules[0].MaxLines != 200 {
		t.Fatalf("expected max lines 200, got %v", rules[0].MaxLines)
	}

	if rules[1].MaxBytes == nil || *rules[1].MaxBytes != 10*1024 {
		t.Fatalf("expected max bytes 10240, got %v", rules[1].MaxBytes)
	}
}

func TestParseByteString(t *testing.T) {
	cases := map[string]int64{
		"1KB":  1024,
		"2MB":  2 * 1024 * 1024,
		"3GB":  3 * 1024 * 1024 * 1024,
		"500B": 500,
	}

	for input, want := range cases {
		got, ok := parseByteString(input)
		if !ok {
			t.Fatalf("expected %s to parse as bytes", input)
		}
		if got != want {
			t.Fatalf("%s: want %d, got %d", input, want, got)
		}
	}
}

func TestMatchPath(t *testing.T) {
	if !matchPath("pkg/foo/bar.go", "*.go") {
		t.Fatal("pattern *.go should match by base name")
	}

	if !matchPath("pkg/foo/bar.go", "pkg/*/bar.go") {
		t.Fatal("pattern with directories should match")
	}

	if matchPath("pkg/foo/bar.txt", "*.go") {
		t.Fatal("should not match mismatched extension")
	}
}
