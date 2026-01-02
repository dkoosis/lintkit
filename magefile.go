//go:build mage

// Tiered quality checks with fo dashboard rendering.
//
// Tiers:
//
//	mage     Build, lint, test (default)
//	mage qa  Full quality: race detection, all linters, govulncheck
//
// Set CLI=1 for console output instead of dashboard.
package main

import (
	"fmt"
	"os"
	"os/exec"
)

// cli returns true if CLI=1 is set (console output instead of dashboard).
func cli() bool {
	return os.Getenv("CLI") != ""
}

// Default target runs standard build + lint + test.
var Default = All

// ----------------------------------------------------------------------------
// Tier 1: Standard (default)
// ----------------------------------------------------------------------------

// All runs build, lint, and test.
func All() error {
	if cli() {
		return allCLI()
	}
	return allDashboard()
}

func allDashboard() error {
	return runFoDashboard(
		// Build - all cmd targets
		"Build/compile:go build ./...",
		"Build/filesize:go build -o bin/filesize ./cmd/filesize",
		"Build/lintkit:go build -o bin/lintkit ./cmd/lintkit",
		"Build/mcp-logscan:go build -o bin/mcp-logscan ./cmd/mcp-logscan",
		"Build/mdsanity:go build -o bin/mdsanity ./cmd/mdsanity",
		// Test
		"Test/unit:go test -json -cover ./...",
		// Lint - essential
		"Lint/vet:go vet ./...",
		"Lint/gofmt:gofmt -l .",
		"Lint/staticcheck:golangci-lint run --allow-parallel-runners --enable-only staticcheck --output.sarif.path=stdout ./...",
		"Lint/gosec:golangci-lint run --allow-parallel-runners --enable-only gosec --output.sarif.path=stdout ./...",
	)
}

func allCLI() error {
	fmt.Println("═══ Build + Lint + Test ═══")
	return runSequential(
		step{"Build", "go", []string{"build", "./..."}},
		step{"Test", "go", []string{"test", "-cover", "./..."}},
		step{"Vet", "go", []string{"vet", "./..."}},
		step{"Gofmt", "gofmt", []string{"-l", "."}},
		step{"Staticcheck", "golangci-lint", []string{"run", "--enable-only", "staticcheck", "./..."}},
		step{"Gosec", "golangci-lint", []string{"run", "--enable-only", "gosec", "./..."}},
	)
}

// ----------------------------------------------------------------------------
// Tier 2: Full QA
// ----------------------------------------------------------------------------

// Qa runs comprehensive quality checks: race detection, all linters, govulncheck.
func Qa() error {
	if cli() {
		return qaCLI()
	}
	return qaDashboard()
}

func qaDashboard() error {
	return runFoDashboard(
		// Build - all cmd targets
		"Build/compile:go build ./...",
		"Build/filesize:go build -o bin/filesize ./cmd/filesize",
		"Build/lintkit:go build -o bin/lintkit ./cmd/lintkit",
		"Build/mcp-logscan:go build -o bin/mcp-logscan ./cmd/mcp-logscan",
		"Build/mdsanity:go build -o bin/mdsanity ./cmd/mdsanity",
		// Test - comprehensive
		"Test/unit:go test -json -cover ./...",
		"Test/race:go test -race -json -timeout=5m ./...",
		// Lint - full suite
		"Lint/vet:go vet ./...",
		"Lint/gofmt:gofmt -l .",
		"Lint/gocyclo:golangci-lint run --allow-parallel-runners --enable-only gocyclo --output.sarif.path=stdout ./...",
		"Lint/goconst:golangci-lint run --allow-parallel-runners --enable-only goconst --output.sarif.path=stdout ./...",
		"Lint/gosec:golangci-lint run --allow-parallel-runners --enable-only gosec --output.sarif.path=stdout ./...",
		"Lint/staticcheck:golangci-lint run --allow-parallel-runners --enable-only staticcheck --output.sarif.path=stdout ./...",
		"Lint/errcheck:golangci-lint run --allow-parallel-runners --enable-only errcheck --output.sarif.path=stdout ./...",
		"Lint/revive:golangci-lint run --allow-parallel-runners --enable-only revive --output.sarif.path=stdout ./...",
		"Lint/ineffassign:golangci-lint run --allow-parallel-runners --enable-only ineffassign --output.sarif.path=stdout ./...",
		"Lint/unconvert:golangci-lint run --allow-parallel-runners --enable-only unconvert --output.sarif.path=stdout ./...",
		"Lint/misspell:golangci-lint run --allow-parallel-runners --enable-only misspell --output.sarif.path=stdout ./...",
		// Security
		"Security/govulncheck:govulncheck ./...",
	)
}

func qaCLI() error {
	fmt.Println("═══ Full QA ═══")
	return runSequential(
		step{"Build", "go", []string{"build", "./..."}},
		step{"Test", "go", []string{"test", "-cover", "./..."}},
		step{"Race", "go", []string{"test", "-race", "-timeout=5m", "./..."}},
		step{"Golangci-lint", "golangci-lint", []string{"run", "./..."}},
		step{"Govulncheck", "govulncheck", []string{"./..."}},
	)
}

// ----------------------------------------------------------------------------
// Utility: Clean
// ----------------------------------------------------------------------------

// Clean removes build artifacts.
func Clean() error {
	fmt.Println("Cleaning build artifacts...")
	if err := os.RemoveAll("bin"); err != nil {
		return err
	}
	return nil
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

type step struct {
	name string
	cmd  string
	args []string
}

func runSequential(steps ...step) error {
	for _, s := range steps {
		fmt.Printf("→ %s\n", s.name)
		cmd := exec.Command(s.cmd, s.args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s failed: %w", s.name, err)
		}
	}
	return nil
}

func runFoDashboard(tasks ...string) error {
	// Find fo binary - prefer local dev build, then PATH
	foBin := os.Getenv("HOME") + "/Projects/fo/bin/fo"
	if _, err := os.Stat(foBin); err != nil {
		var lookupErr error
		foBin, lookupErr = exec.LookPath("fo")
		if lookupErr != nil {
			return fmt.Errorf("fo binary not found at ~/Projects/fo/bin/fo or in PATH")
		}
	}

	// Build task args
	args := []string{"--dashboard"}
	for _, t := range tasks {
		args = append(args, "--task", t)
	}

	// Run dashboard with TTY attached
	cmd := exec.Command(foBin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PATH="+os.Getenv("PATH")+":"+os.Getenv("HOME")+"/go/bin")
	return cmd.Run()
}
