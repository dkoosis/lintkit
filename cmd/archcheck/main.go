// Command archcheck validates architecture boundaries in Go projects.
//
// It runs multiple checks:
//   - go-arch-lint for component dependency rules
//   - Data ownership validation (Single Writer Rule)
//   - Layer violation detection (domain/infra boundaries)
//   - Interface boundary checks
//   - Dependency graph analysis (cycles, deep chains)
//   - Handler isolation (e.g., MCP tools don't import each other)
//   - Configuration coverage validation
//
// Usage:
//
//	archcheck                         # human-readable output
//	archcheck -format=check           # lintkit-check JSON
//	archcheck -format=sarif           # SARIF JSON
//	archcheck -config=.go-arch-lint.yml
//	archcheck -module=github.com/org/repo
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/check"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

// Violation represents an architecture violation.
type Violation struct {
	Rule       string
	File       string
	Line       int
	Message    string
	Severity   string // error, warning
	Suggestion string
	DocLink    string
}

// CheckResult holds status for a specific check category.
type CheckResult struct {
	Name   string
	OK     bool
	Errors int
	Warns  int
}

// Config holds runtime configuration.
type Config struct {
	Format        string
	Verbose       bool
	ShowFixes     bool
	SkipArchLint  bool
	ConfigPath    string
	ModulePrefix  string
	ArchDocsURL   string
	InvDocsURL    string
}

var config Config

// packageInfo holds import information for a package.
type packageInfo struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
	Dir        string   `json:"Dir"`
	Name       string   `json:"Name"`
}

func main() {
	flag.StringVar(&config.Format, "format", "human", "output format: sarif, check, human")
	flag.BoolVar(&config.Verbose, "verbose", false, "show verbose output")
	flag.BoolVar(&config.ShowFixes, "fix", false, "show fix suggestions")
	flag.BoolVar(&config.SkipArchLint, "skip-arch-lint", false, "skip go-arch-lint (useful if not installed)")
	flag.StringVar(&config.ConfigPath, "config", ".go-arch-lint.yml", "path to go-arch-lint config")
	flag.StringVar(&config.ModulePrefix, "module", "", "module prefix (auto-detected from go.mod if empty)")
	flag.StringVar(&config.ArchDocsURL, "arch-docs", "docs/architecture.md", "architecture docs URL for violations")
	flag.StringVar(&config.InvDocsURL, "inv-docs", "docs/INVARIANTS.md", "invariants docs URL for violations")
	flag.Parse()

	// Auto-detect module prefix from go.mod if not provided
	if config.ModulePrefix == "" {
		config.ModulePrefix = detectModulePrefix()
	}
	// Ensure trailing slash for prefix matching
	if config.ModulePrefix != "" && !strings.HasSuffix(config.ModulePrefix, "/") {
		config.ModulePrefix += "/"
	}

	var allViolations []Violation
	results := make([]CheckResult, 0, 7)

	// Run go-arch-lint
	if !config.SkipArchLint {
		violations := runGoArchLint()
		result := CheckResult{Name: "go-arch-lint", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check data ownership rules
	{
		violations := checkDataOwnership()
		result := CheckResult{Name: "data-ownership", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check layer violations
	{
		violations := checkLayerViolations()
		result := CheckResult{Name: "layer-violations", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check interface boundaries
	{
		violations := checkInterfaceBoundaries()
		result := CheckResult{Name: "interface-boundary", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check dependency graph
	{
		violations := checkDependencyGraph()
		result := CheckResult{Name: "dependency-graph", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check handler isolation
	{
		violations := checkHandlerIsolation()
		result := CheckResult{Name: "handler-isolation", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	// Check configuration coverage
	{
		violations := checkConfigCoverage()
		result := CheckResult{Name: "config-coverage", OK: true}
		for _, v := range violations {
			if v.Severity == "error" {
				result.Errors++
			} else {
				result.Warns++
			}
		}
		result.OK = result.Errors == 0
		results = append(results, result)
		allViolations = append(allViolations, violations...)
	}

	switch config.Format {
	case "sarif":
		outputSARIF(allViolations)
	case "check":
		outputCheck(allViolations, results)
	default:
		outputHuman(allViolations, results)
	}

	// Exit with error if any errors found
	for _, v := range allViolations {
		if v.Severity == "error" {
			os.Exit(1)
		}
	}
}

func detectModulePrefix() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimPrefix(line, "module ")
		}
	}
	return ""
}

func runGoArchLint() []Violation {
	cmd := exec.Command("go-arch-lint", "check", "--json")
	out, err := cmd.Output()
	if err == nil {
		return nil // No violations
	}

	// Check if go-arch-lint is not installed
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return []Violation{{
			Rule:       "go-arch-lint",
			Message:    "go-arch-lint not found. Install with: go install github.com/fe3dback/go-arch-lint/v4@latest",
			Severity:   "warning",
			Suggestion: "Install go-arch-lint or run with -skip-arch-lint flag",
		}}
	}

	// Parse JSON output for violations
	var result struct {
		Payload struct {
			ArchWarningsDeps []struct {
				ComponentName      string `json:"ComponentName"`
				FileRelativePath   string `json:"FileRelativePath"`
				ResolvedImportName string `json:"ResolvedImportName"`
				Reference          struct {
					Line int `json:"Line"`
				} `json:"Reference"`
			} `json:"ArchWarningsDeps"`
		} `json:"Payload"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return []Violation{{
			Rule:     "go-arch-lint",
			Message:  "Failed to parse go-arch-lint output",
			Severity: "error",
		}}
	}

	violations := make([]Violation, 0, len(result.Payload.ArchWarningsDeps))
	for _, w := range result.Payload.ArchWarningsDeps {
		violations = append(violations, Violation{
			Rule:       "component-dependency",
			File:       w.FileRelativePath,
			Line:       w.Reference.Line,
			Message:    fmt.Sprintf("Component %s should not depend on %s", w.ComponentName, w.ResolvedImportName),
			Severity:   "error",
			Suggestion: "Extract shared types to a common package or use interfaces",
			DocLink:    config.ArchDocsURL,
		})
	}

	return violations
}

func checkDataOwnership() []Violation {
	var violations []Violation

	// Check for hardcoded database paths in wrong places
	checks := []struct {
		dir         string
		forbidden   []string
		description string
		suggestion  string
	}{
		{
			dir:         "internal/proc/mnemd",
			forbidden:   []string{"TelemetryDB", "telemetry_db", "telemetry.db"},
			description: "mnemd should not reference telemetry database",
			suggestion:  "mnemd only processes knowledge.db via okgd APIs",
		},
	}

	for _, chk := range checks {
		if _, err := os.Stat(chk.dir); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(chk.dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}

			content, err := os.ReadFile(path) //nolint:gosec // path from walk
			if err != nil {
				return nil
			}
			contentStr := string(content)

			for _, pattern := range chk.forbidden {
				if containsInCode(contentStr, pattern) {
					violations = append(violations, Violation{
						Rule:       "data-ownership",
						File:       path,
						Message:    chk.description + " (found: " + pattern + ")",
						Severity:   "warning",
						Suggestion: chk.suggestion,
						DocLink:    config.InvDocsURL + "#single-writer-per-table",
					})
					break // One violation per file is enough
				}
			}
			return nil
		})
		if err != nil {
			continue
		}
	}

	return violations
}

// checkLayerViolations detects violations of layered architecture.
func checkLayerViolations() []Violation {
	var violations []Violation

	// Infrastructure packages that domain/service layers should not import directly
	infraPackages := []string{
		"database/sql",
		"github.com/mattn/go-sqlite3",
		"modernc.org/sqlite",
		"net/http",
	}

	// Domain packages that should not import infrastructure
	domainPaths := []string{
		"internal/kg/nuggets",
		"internal/kg/schema",
		"internal/domain",
	}

	for _, domainPath := range domainPaths {
		if _, err := os.Stat(domainPath); os.IsNotExist(err) {
			continue
		}

		domainViolations := checkPackageImportsGeneric(
			domainPath,
			infraPackages,
			func(file, imp string) Violation {
				return Violation{
					Rule:       "layer-violation",
					File:       file,
					Message:    fmt.Sprintf("Domain package imports infrastructure: %s", imp),
					Severity:   "error",
					Suggestion: "Domain logic should depend on interfaces, not concrete infrastructure.",
					DocLink:    config.ArchDocsURL,
				}
			},
		)
		violations = append(violations, domainViolations...)
	}

	// Check handler storage access
	violations = append(violations, checkHandlerStorageAccess()...)

	// Check cross-daemon dependencies
	violations = append(violations, checkCrossDaemonDependencies()...)

	return violations
}

func checkPackageImportsGeneric(pkgPath string, forbiddenImports []string, makeViolation func(string, string) Violation) []Violation {
	var violations []Violation

	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		return nil
	}

	err := filepath.Walk(pkgPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}

		for _, imp := range file.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range forbiddenImports {
				if impPath == forbidden || strings.HasPrefix(impPath, forbidden+"/") {
					v := makeViolation(path, impPath)
					v.Line = fset.Position(imp.Pos()).Line
					violations = append(violations, v)
				}
			}
		}
		return nil
	})
	if err != nil && config.Verbose {
		fmt.Fprintf(os.Stderr, "warning: walking %s: %v\n", pkgPath, err)
	}

	return violations
}

func checkHandlerStorageAccess() []Violation {
	var violations []Violation

	handlerPath := "internal/mcp/server"
	storagePackages := []string{
		config.ModulePrefix + "internal/kgstore",
		config.ModulePrefix + "internal/kg/db",
	}

	exceptions := map[string]bool{
		"kg_storage.go":   true,
		"core_service.go": true,
	}

	if _, err := os.Stat(handlerPath); os.IsNotExist(err) {
		return nil
	}

	err := filepath.Walk(handlerPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		filename := filepath.Base(path)
		if exceptions[filename] {
			return nil
		}

		if !strings.HasSuffix(filename, "_handler.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}

		for _, imp := range file.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			for _, storagePkg := range storagePackages {
				if impPath == storagePkg || strings.HasPrefix(impPath, storagePkg+"/") {
					violations = append(violations, Violation{
						Rule:       "layer-violation",
						File:       path,
						Line:       fset.Position(imp.Pos()).Line,
						Message:    fmt.Sprintf("Handler directly imports storage: %s", impPath),
						Severity:   "warning",
						Suggestion: "Handlers should use service layer, not storage directly.",
						DocLink:    config.ArchDocsURL,
					})
				}
			}
		}
		return nil
	})
	if err != nil && config.Verbose {
		fmt.Fprintf(os.Stderr, "warning: walking %s: %v\n", handlerPath, err)
	}

	return violations
}

func checkCrossDaemonDependencies() []Violation {
	var violations []Violation

	daemonPaths := map[string][]string{
		"mnemd": {"internal/proc/mnemd"},
		"osigd": {"internal/proc/osigd"},
		"okgd":  {"internal/proc/okgd"},
	}

	for daemonName, paths := range daemonPaths {
		for _, path := range paths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}

			for otherDaemon, otherPaths := range daemonPaths {
				if otherDaemon == daemonName {
					continue
				}

				for _, otherPath := range otherPaths {
					forbiddenImport := config.ModulePrefix + otherPath
					checkViolations := checkPackageImportsGeneric(
						path,
						[]string{forbiddenImport},
						func(file, imp string) Violation {
							return Violation{
								Rule:       "cross-daemon",
								File:       file,
								Message:    fmt.Sprintf("%s imports %s internals", daemonName, otherDaemon),
								Severity:   "error",
								Suggestion: "Daemons should communicate via APIs or shared interfaces",
								DocLink:    config.ArchDocsURL,
							}
						},
					)
					violations = append(violations, checkViolations...)
				}
			}
		}
	}

	return violations
}

// checkInterfaceBoundaries ensures key service interfaces exist.
func checkInterfaceBoundaries() []Violation {
	var violations []Violation

	interfaceChecks := []struct {
		path       string
		interfaces []string
		purpose    string
	}{
		{
			path:       "internal/kg/mgmt",
			interfaces: []string{"Manager", "Store", "Searcher"},
			purpose:    "KG service layer should expose interfaces for testability",
		},
		{
			path:       "internal/core",
			interfaces: []string{"KGClient"},
			purpose:    "Core should define interface for KG client",
		},
	}

	for _, chk := range interfaceChecks {
		if _, err := os.Stat(chk.path); os.IsNotExist(err) {
			continue
		}

		foundInterfaces := findInterfacesInPackage(chk.path)
		for _, required := range chk.interfaces {
			found := false
			for _, iface := range foundInterfaces {
				if iface == required {
					found = true
					break
				}
			}
			if !found && config.Verbose {
				violations = append(violations, Violation{
					Rule:       "interface-boundary",
					File:       chk.path,
					Message:    fmt.Sprintf("Missing recommended interface: %s (%s)", required, chk.purpose),
					Severity:   "warning",
					Suggestion: "Define interface to enable testing and loose coupling",
				})
			}
		}
	}

	return violations
}

func findInterfacesInPackage(pkgPath string) []string {
	var interfaces []string

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgPath, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface {
						interfaces = append(interfaces, typeSpec.Name.Name)
					}
				}
			}
		}
	}

	return interfaces
}

// checkDependencyGraph analyzes import patterns for cycles and deep chains.
func checkDependencyGraph() []Violation {
	var violations []Violation //nolint:prealloc

	graph := buildImportGraph()

	// Check for circular dependencies
	cycles := findCycles(graph)
	for _, cycle := range cycles {
		violations = append(violations, Violation{
			Rule:       "circular-dependency",
			Message:    fmt.Sprintf("Circular dependency detected: %s", strings.Join(cycle, " -> ")),
			Severity:   "error",
			Suggestion: "Break cycle by extracting shared types to a common package or using interfaces",
			DocLink:    config.ArchDocsURL,
		})
	}

	// Check for deep dependency chains (max depth 5)
	const maxDepth = 5
	deepChains := findDeepChains(graph, maxDepth)
	for _, chain := range deepChains {
		violations = append(violations, Violation{
			Rule:       "deep-dependency",
			Message:    fmt.Sprintf("Deep dependency chain (%d levels): %s", len(chain), formatChain(chain)),
			Severity:   "warning",
			Suggestion: "Consider flattening dependencies or introducing abstraction layers",
		})
	}

	return violations
}

func buildImportGraph() map[string][]string {
	graph := make(map[string][]string)

	cmd := exec.Command("go", "list", "-json", "./internal/...")
	out, err := cmd.Output()
	if err != nil {
		return graph
	}

	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for decoder.More() {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err != nil {
			continue
		}

		var internalImports []string
		for _, imp := range pkg.Imports {
			if strings.HasPrefix(imp, config.ModulePrefix+"internal/") {
				internalImports = append(internalImports, imp)
			}
		}

		if len(internalImports) > 0 {
			graph[pkg.ImportPath] = internalImports
		}
	}

	return graph
}

func findCycles(graph map[string][]string) [][]string {
	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	path := make([]string, 0)

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, neighbor := range graph[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				cycleStart := 0
				for i, n := range path {
					if n == neighbor {
						cycleStart = i
						break
					}
				}
				cycle := append([]string{}, path[cycleStart:]...)
				cycle = append(cycle, neighbor)
				cycles = append(cycles, cycle)
			}
		}

		path = path[:len(path)-1]
		recStack[node] = false
		return false
	}

	for node := range graph {
		if !visited[node] {
			dfs(node)
		}
	}

	return cycles
}

func findDeepChains(graph map[string][]string, maxDepth int) [][]string {
	var deepChains [][]string
	visited := make(map[string]int)

	var dfs func(node string, path []string, depth int)
	dfs = func(node string, path []string, depth int) {
		if depth > maxDepth {
			chain := make([]string, len(path))
			copy(chain, path)
			deepChains = append(deepChains, chain)
			return
		}

		if prevDepth, ok := visited[node]; ok && depth <= prevDepth {
			return
		}
		visited[node] = depth

		for _, neighbor := range graph[node] {
			newPath := append(path, neighbor) //nolint:gocritic
			dfs(neighbor, newPath, depth+1)
		}
	}

	for node := range graph {
		dfs(node, []string{node}, 1)
	}

	// Deduplicate and limit
	seen := make(map[string]bool)
	var unique [][]string
	for _, chain := range deepChains {
		key := strings.Join(chain, "->")
		if !seen[key] && len(unique) < 5 {
			seen[key] = true
			unique = append(unique, chain)
		}
	}

	return unique
}

func formatChain(chain []string) string {
	short := make([]string, 0, len(chain))
	for _, pkg := range chain {
		short = append(short, strings.TrimPrefix(pkg, config.ModulePrefix))
	}
	return strings.Join(short, " -> ")
}

// checkHandlerIsolation ensures handler packages don't import each other.
func checkHandlerIsolation() []Violation {
	var violations []Violation

	handlerDirs := []string{
		"internal/mcp/server/handlers/admin",
		"internal/mcp/server/handlers/files",
		"internal/mcp/server/handlers/knowledge",
		"internal/mcp/server/handlers/shell",
		"internal/mcp/server/handlers/symbols",
		"internal/mcp/server/handlers/workspace",
	}

	for _, handlerDir := range handlerDirs {
		if _, err := os.Stat(handlerDir); os.IsNotExist(err) {
			continue
		}

		for _, otherDir := range handlerDirs {
			if otherDir == handlerDir {
				continue
			}

			forbiddenImport := config.ModulePrefix + otherDir
			checkViolations := checkPackageImportsGeneric(
				handlerDir,
				[]string{forbiddenImport},
				func(file, imp string) Violation {
					return Violation{
						Rule:       "handler-isolation",
						File:       file,
						Message:    fmt.Sprintf("Handler imports another handler package: %s", imp),
						Severity:   "warning",
						Suggestion: "Extract shared logic to a core or shared service package",
						DocLink:    config.ArchDocsURL,
					}
				},
			)
			violations = append(violations, checkViolations...)
		}
	}

	return violations
}

// checkConfigCoverage verifies .go-arch-lint.yml covers all packages.
func checkConfigCoverage() []Violation {
	var violations []Violation

	content, err := os.ReadFile(config.ConfigPath) //nolint:gosec // user-provided path
	if err != nil {
		if os.IsNotExist(err) {
			return []Violation{{
				Rule:       "config-coverage",
				Message:    fmt.Sprintf("%s not found", config.ConfigPath),
				Severity:   "warning",
				Suggestion: "Create go-arch-lint config to define component boundaries",
				DocLink:    "https://github.com/fe3dback/go-arch-lint",
			}}
		}
		return nil
	}

	coveredPatterns := extractComponentPatterns(string(content))
	allPackages := getInternalPackages()

	for _, pkg := range allPackages {
		relPath := strings.TrimPrefix(pkg, config.ModulePrefix+"internal/")
		if !isPackageCovered(relPath, coveredPatterns) {
			if isExcluded(relPath, string(content)) {
				continue
			}
			violations = append(violations, Violation{
				Rule:       "config-coverage",
				File:       config.ConfigPath,
				Message:    fmt.Sprintf("Package not covered by any component: internal/%s", relPath),
				Severity:   "warning",
				Suggestion: "Add package to appropriate component in config",
			})
		}
	}

	return violations
}

func extractComponentPatterns(content string) []string {
	var patterns []string

	inBlockRe := regexp.MustCompile(`in:\s*\n((?:\s+-\s+[^\n]+\n?)+)`)
	matches := inBlockRe.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) > 1 {
			lines := strings.Split(match[1], "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "- ") {
					pattern := strings.TrimPrefix(line, "- ")
					pattern = strings.TrimSpace(pattern)
					patterns = append(patterns, pattern)
				}
			}
		}
	}

	return patterns
}

func isPackageCovered(pkgPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(pkgPath, pattern) {
			return true
		}
	}
	return false
}

func matchPattern(path, pattern string) bool {
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(path, prefix+"/") && !strings.Contains(strings.TrimPrefix(path, prefix+"/"), "/")
	}
	return path == pattern
}

func isExcluded(pkgPath, content string) bool {
	excludePatterns := []string{
		"devtools",
		"testdata",
		"testutil",
		"testing",
		"architecture",
	}

	for _, pattern := range excludePatterns {
		if strings.Contains(pkgPath, pattern) {
			return true
		}
	}

	return false
}

func getInternalPackages() []string {
	cmd := exec.Command("go", "list", "./internal/...")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var packages []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		packages = append(packages, scanner.Text())
	}

	return packages
}

// containsInCode checks if pattern appears in code (not just comments).
func containsInCode(content, pattern string) bool {
	lines := strings.Split(content, "\n")
	inBlockComment := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, "/*") {
			inBlockComment = true
		}
		if strings.Contains(trimmed, "*/") {
			inBlockComment = false
			continue
		}

		if inBlockComment || strings.HasPrefix(trimmed, "//") {
			continue
		}

		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// Output functions

func outputSARIF(violations []Violation) {
	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-archcheck"}},
	}

	for _, v := range violations {
		level := "warning"
		if v.Severity == "error" {
			level = "error"
		}

		result := sarif.Result{
			RuleID:  v.Rule,
			Level:   level,
			Message: sarif.Message{Text: v.Message},
		}

		if v.File != "" {
			loc := sarif.Location{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: filepath.ToSlash(v.File)},
				},
			}
			if v.Line > 0 {
				loc.PhysicalLocation.Region = &sarif.Region{StartLine: v.Line}
			}
			result.Locations = []sarif.Location{loc}
		}

		run.Results = append(run.Results, result)
	}

	log.Runs = append(log.Runs, run)
	enc := sarif.NewEncoder(os.Stdout)
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding SARIF: %v\n", err)
		os.Exit(1)
	}
}

func outputCheck(violations []Violation, results []CheckResult) {
	report := check.NewReport("archcheck")

	// Add metrics for each check category
	totalErrors := 0
	totalWarns := 0
	for _, r := range results {
		totalErrors += r.Errors
		totalWarns += r.Warns
	}

	report.Metrics = []check.Metric{
		{Name: "errors", Value: float64(totalErrors), Threshold: 0, Op: check.OpLTE},
		{Name: "warnings", Value: float64(totalWarns)},
		{Name: "checks_passed", Value: float64(countPassed(results)), Unit: fmt.Sprintf("/%d", len(results))},
	}

	// Add items for violations
	for _, v := range violations {
		sev := check.SeverityWarn
		if v.Severity == "error" {
			sev = check.SeverityError
		}

		item := check.Item{
			Severity: sev,
			Label:    v.Rule,
			Message:  v.Message,
			Path:     v.File,
		}
		if config.ShowFixes && v.Suggestion != "" {
			item.Fix = v.Suggestion
		}

		report.Items = append(report.Items, item)
	}

	// Set summary
	if totalErrors == 0 && totalWarns == 0 {
		report.Summary = "All architecture checks passed"
	} else {
		var parts []string
		if totalErrors > 0 {
			parts = append(parts, fmt.Sprintf("%d errors", totalErrors))
		}
		if totalWarns > 0 {
			parts = append(parts, fmt.Sprintf("%d warnings", totalWarns))
		}
		report.Summary = strings.Join(parts, ", ")
	}

	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func countPassed(results []CheckResult) int {
	count := 0
	for _, r := range results {
		if r.OK {
			count++
		}
	}
	return count
}

func outputHuman(violations []Violation, results []CheckResult) {
	report := check.NewReport("archcheck")

	// Build items from check results first (as category summaries)
	for _, r := range results {
		sev := check.SeverityInfo
		value := "PASS"
		if r.Errors > 0 {
			sev = check.SeverityError
			value = fmt.Sprintf("%d errors", r.Errors)
		} else if r.Warns > 0 {
			sev = check.SeverityWarn
			value = fmt.Sprintf("%d warnings", r.Warns)
		}

		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    r.Name,
			Value:    value,
		})
	}

	// Add violation details if any
	if len(violations) > 0 {
		// Group by rule
		byRule := make(map[string][]Violation)
		for _, v := range violations {
			byRule[v.Rule] = append(byRule[v.Rule], v)
		}

		rules := make([]string, 0, len(byRule))
		for rule := range byRule {
			rules = append(rules, rule)
		}
		sort.Strings(rules)

		for _, rule := range rules {
			for _, v := range byRule[rule] {
				sev := check.SeverityWarn
				if v.Severity == "error" {
					sev = check.SeverityError
				}

				item := check.Item{
					Severity: sev,
					Label:    fmt.Sprintf("[%s]", rule),
					Message:  v.Message,
					Path:     v.File,
				}
				if config.ShowFixes && v.Suggestion != "" {
					item.Fix = v.Suggestion
				}

				report.Items = append(report.Items, item)
			}
		}
	}

	// Metrics
	totalErrors := 0
	totalWarns := 0
	for _, r := range results {
		totalErrors += r.Errors
		totalWarns += r.Warns
	}

	report.Metrics = []check.Metric{
		{Name: "errors", Value: float64(totalErrors)},
		{Name: "warnings", Value: float64(totalWarns)},
		{Name: "checks", Value: float64(len(results))},
	}

	// Summary
	if totalErrors == 0 && totalWarns == 0 {
		report.Summary = "All architecture checks passed"
		report.Status = check.StatusPass
	} else if totalErrors > 0 {
		report.Summary = fmt.Sprintf("%d errors, %d warnings", totalErrors, totalWarns)
		report.Status = check.StatusFail
	} else {
		report.Summary = fmt.Sprintf("%d warnings", totalWarns)
		report.Status = check.StatusWarn
	}

	cfg := check.DefaultHumanConfig()
	cfg.Purpose = "Validate architecture boundaries and dependency rules"
	report.WriteHuman(os.Stdout, cfg)

	if !config.ShowFixes && hasFixSuggestions(violations) {
		fmt.Println("\nTip: Run with -fix to see suggestions for fixing violations")
	}
}

func hasFixSuggestions(violations []Violation) bool {
	for _, v := range violations {
		if v.Suggestion != "" {
			return true
		}
	}
	return false
}
