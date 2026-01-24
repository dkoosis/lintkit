// Command jtbd reports test coverage mapped to user goals (Jobs To Be Done).
//
// It scans test files for "Serves: JTBD-X" annotations and reports which
// user goals have test coverage. This makes the connection between tests
// and user value explicit and visible.
//
// Usage:
//
//	jtbd                         # default human output
//	jtbd -format=check           # JSON check format
//	jtbd -format=sarif           # SARIF format
//	jtbd -jtbd=docs/jtbd.yaml    # custom JTBD file
//	jtbd -validate               # validate annotations match real tests
//	jtbd -gaps                   # show gap analysis with implementation hints
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/check"
	"github.com/dkoosis/lintkit/pkg/sarif"
	"gopkg.in/yaml.v3"
)

// JTBDFile represents the JTBD definitions file structure.
type JTBDFile struct {
	Version int             `yaml:"version"`
	Jobs    map[string]JTBD `yaml:"jobs"`
}

// JTBD represents a single job-to-be-done.
type JTBD struct {
	Principle string   `yaml:"principle"`
	Statement string   `yaml:"statement"`
	Keywords  []string `yaml:"keywords"`
}

// TestType categorizes tests by their scope.
type TestType string

const (
	TestTypeUnit        TestType = "unit"
	TestTypeIntegration TestType = "integration"
	TestTypeE2E         TestType = "e2e"
)

// TestInfo captures information about a single test function.
type TestInfo struct {
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Type     TestType `json:"type"`
	JTBDIDs  []string `json:"jtbd_ids,omitempty"`
	Runnable bool     `json:"runnable"`
}

// AnnotationInfo tracks JTBD annotation details.
type AnnotationInfo struct {
	JTBDID       string `json:"jtbd_id"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	TestFunction string `json:"test_function,omitempty"`
	Valid        bool   `json:"valid"`
	Reason       string `json:"reason,omitempty"`
}

// Coverage tracks which test files serve each JTBD.
type Coverage struct {
	JTBDID           string     `json:"jtbd_id"`
	Principle        string     `json:"principle"`
	Statement        string     `json:"statement"`
	Keywords         []string   `json:"keywords,omitempty"`
	TestFiles        []string   `json:"test_files"`
	Tests            []TestInfo `json:"tests,omitempty"`
	Count            int        `json:"count"`
	UnitTests        int        `json:"unit_tests"`
	IntegrationTests int        `json:"integration_tests"`
	E2ETests         int        `json:"e2e_tests"`
	DepthScore       int        `json:"depth_score"`
}

// GapAnalysis provides hints for uncovered JTBDs.
type GapAnalysis struct {
	JTBDID          string   `json:"jtbd_id"`
	Statement       string   `json:"statement"`
	Keywords        []string `json:"keywords"`
	ImplementedIn   []string `json:"implemented_in,omitempty"`
	SuggestedTestIn []string `json:"suggested_test_in,omitempty"`
	KeywordMatches  int      `json:"keyword_matches"`
	MissingKeywords bool     `json:"missing_keywords"`
}

// ValidationResult reports annotation validation issues.
type ValidationResult struct {
	TotalAnnotations int              `json:"total_annotations"`
	ValidAnnotations int              `json:"valid_annotations"`
	StaleAnnotations []AnnotationInfo `json:"stale_annotations,omitempty"`
	InvalidJTBDRefs  []AnnotationInfo `json:"invalid_jtbd_refs,omitempty"`
	JTBDWarnings     []string         `json:"jtbd_warnings,omitempty"`
}

// ScanResult holds all information from scanning test files.
type ScanResult struct {
	testFiles     []string
	tests         map[string][]TestInfo
	annotations   []AnnotationInfo
	fileMappings  map[string][]string
	unmappedFiles []string
}

// internalReport is the internal structure before converting to check.Report.
type internalReport struct {
	TotalJTBDs      int
	CoveredJTBDs    int
	TotalTests      int
	MappedTests     int
	TotalDepthScore int
	Coverage        []Coverage
	Unmapped        []string
	Validation      *ValidationResult
	Gaps            []GapAnalysis
}

var servesPattern = regexp.MustCompile(`(?i)Serves:\s*(JTBD-\d+(?:\s*,\s*JTBD-\d+)*)`)

func main() {
	jtbdPath := flag.String("jtbd", "docs/jtbd.yaml", "path to JTBD definitions file")
	format := flag.String("format", "human", "output format: sarif, check, human")
	root := flag.String("root", ".", "root directory to scan for tests")
	showUnmapped := flag.Bool("unmapped", false, "show unmapped test files")
	validate := flag.Bool("validate", false, "validate annotations match real tests")
	gaps := flag.Bool("gaps", false, "show gap analysis for uncovered JTBDs")
	flag.Parse()

	// Load JTBD definitions
	jtbds, err := loadJTBDs(*jtbdPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading JTBDs: %v\n", err)
		os.Exit(1)
	}

	// Validate JTBD definitions
	jtbdWarnings := validateJTBDDefinitions(jtbds)

	// Scan test files with full parsing
	scanResult, err := scanTestFilesWithValidation(*root, jtbds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning test files: %v\n", err)
		os.Exit(1)
	}

	// Build internal report
	report := buildInternalReport(jtbds, scanResult, *showUnmapped)

	// Add validation results
	if *validate {
		validation := buildValidation(scanResult, jtbds)
		validation.JTBDWarnings = jtbdWarnings
		report.Validation = validation
	}

	// Add gap analysis
	if *gaps {
		report.Gaps = analyzeGaps(*root, jtbds, report.Coverage)
	}

	// Output
	switch *format {
	case "sarif":
		outputSARIF(report)
	case "check":
		outputCheck(report)
	default:
		outputHuman(report)
	}
}

func loadJTBDs(path string) (*JTBDFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JTBD file: %w", err)
	}

	var jtbds JTBDFile
	if err := yaml.Unmarshal(data, &jtbds); err != nil {
		return nil, fmt.Errorf("parse JTBD file: %w", err)
	}

	return &jtbds, nil
}

func validateJTBDDefinitions(jtbds *JTBDFile) []string {
	var warnings []string
	for id, job := range jtbds.Jobs {
		if len(job.Keywords) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s has no keywords (auto-matching disabled)", id))
		}
		if job.Statement == "" {
			warnings = append(warnings, fmt.Sprintf("%s has no statement", id))
		}
		if job.Principle == "" {
			warnings = append(warnings, fmt.Sprintf("%s has no principle", id))
		}
	}
	sort.Strings(warnings)
	return warnings
}

func scanTestFilesWithValidation(root string, jtbds *JTBDFile) (*ScanResult, error) {
	result := &ScanResult{
		tests:        make(map[string][]TestInfo),
		fileMappings: make(map[string][]string),
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip vendor, .git, .codex directories
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == ".git" || name == ".codex" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process _test.go files
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		result.testFiles = append(result.testFiles, relPath)

		// Parse file for tests and annotations
		tests, annotations := parseTestFile(path, relPath, jtbds)
		if len(tests) > 0 {
			result.tests[relPath] = tests
		}
		result.annotations = append(result.annotations, annotations...)

		// Extract JTBD IDs for file-level mapping
		jtbdIDs := extractJTBDAnnotations(path)
		if len(jtbdIDs) > 0 {
			result.fileMappings[relPath] = jtbdIDs
		} else {
			result.unmappedFiles = append(result.unmappedFiles, relPath)
		}

		return nil
	})

	return result, err
}

func parseTestFile(path, relPath string, jtbds *JTBDFile) ([]TestInfo, []AnnotationInfo) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, nil
	}

	tests := make([]TestInfo, 0, len(node.Decls))
	var annotations []AnnotationInfo
	testFuncs := make(map[string]bool)

	// Collect all test functions
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		name := fn.Name.Name
		if !isTestFunction(name) {
			continue
		}

		testFuncs[name] = true
		testType := classifyTest(name, relPath)

		info := TestInfo{
			Name:     name,
			File:     relPath,
			Line:     fset.Position(fn.Pos()).Line,
			Type:     testType,
			Runnable: true,
		}

		// Check for JTBD annotations on this function
		if fn.Doc != nil {
			ids := extractFromComment(fn.Doc.Text())
			info.JTBDIDs = ids
			for _, id := range ids {
				valid := true
				reason := ""
				if _, exists := jtbds.Jobs[id]; !exists {
					valid = false
					reason = fmt.Sprintf("JTBD %s not defined in jtbd.yaml", id)
				}
				annotations = append(annotations, AnnotationInfo{
					JTBDID:       id,
					File:         relPath,
					Line:         fset.Position(fn.Doc.Pos()).Line,
					TestFunction: name,
					Valid:        valid,
					Reason:       reason,
				})
			}
		}

		tests = append(tests, info)
	}

	// Check file-level and package-level annotations
	fileAnnotations := extractAllAnnotationsWithPosition(node, fset, relPath, jtbds)
	annotations = append(annotations, fileAnnotations...)

	return tests, annotations
}

func extractAllAnnotationsWithPosition(node *ast.File, fset *token.FileSet, relPath string, jtbds *JTBDFile) []AnnotationInfo {
	var annotations []AnnotationInfo

	// Check package doc comment
	if node.Doc != nil {
		ids := extractFromComment(node.Doc.Text())
		for _, id := range ids {
			valid := true
			reason := ""
			if _, exists := jtbds.Jobs[id]; !exists {
				valid = false
				reason = fmt.Sprintf("JTBD %s not defined in jtbd.yaml", id)
			}
			annotations = append(annotations, AnnotationInfo{
				JTBDID: id,
				File:   relPath,
				Line:   fset.Position(node.Doc.Pos()).Line,
				Valid:  valid,
				Reason: reason,
			})
		}
	}

	// Check all file-level comments
	for _, cg := range node.Comments {
		ids := extractFromComment(cg.Text())
		for _, id := range ids {
			valid := true
			reason := ""
			if _, exists := jtbds.Jobs[id]; !exists {
				valid = false
				reason = fmt.Sprintf("JTBD %s not defined in jtbd.yaml", id)
			}
			annotations = append(annotations, AnnotationInfo{
				JTBDID: id,
				File:   relPath,
				Line:   fset.Position(cg.Pos()).Line,
				Valid:  valid,
				Reason: reason,
			})
		}
	}

	return annotations
}

func isTestFunction(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}

func classifyTest(testName, filePath string) TestType {
	lowerName := strings.ToLower(testName)
	lowerPath := strings.ToLower(filePath)

	// E2E tests
	if strings.Contains(lowerName, "e2e") ||
		strings.Contains(lowerPath, "e2e") ||
		strings.Contains(lowerName, "endtoend") ||
		strings.Contains(lowerPath, "endtoend") {
		return TestTypeE2E
	}

	// Integration tests
	if strings.Contains(lowerName, "integration") ||
		strings.Contains(lowerPath, "integration") ||
		strings.Contains(lowerName, "_integration") ||
		strings.HasSuffix(lowerPath, "_integration_test.go") {
		return TestTypeIntegration
	}

	// Workflow tests are integration
	if strings.Contains(lowerName, "workflow") {
		return TestTypeIntegration
	}

	return TestTypeUnit
}

func extractJTBDAnnotations(path string) []string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	var jtbdIDs []string
	seen := make(map[string]bool)

	// Check package doc comment
	if node.Doc != nil {
		ids := extractFromComment(node.Doc.Text())
		for _, id := range ids {
			if !seen[id] {
				jtbdIDs = append(jtbdIDs, id)
				seen[id] = true
			}
		}
	}

	// Check file-level comments
	for _, cg := range node.Comments {
		ids := extractFromComment(cg.Text())
		for _, id := range ids {
			if !seen[id] {
				jtbdIDs = append(jtbdIDs, id)
				seen[id] = true
			}
		}
	}

	// Check function doc comments
	for _, decl := range node.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Doc != nil {
			ids := extractFromComment(fn.Doc.Text())
			for _, id := range ids {
				if !seen[id] {
					jtbdIDs = append(jtbdIDs, id)
					seen[id] = true
				}
			}
		}
	}

	return jtbdIDs
}

func extractFromComment(text string) []string {
	matches := servesPattern.FindAllStringSubmatch(text, -1)
	var ids []string
	for _, match := range matches {
		if len(match) > 1 {
			parts := strings.Split(match[1], ",")
			for _, p := range parts {
				id := strings.TrimSpace(p)
				if id != "" {
					ids = append(ids, strings.ToUpper(id))
				}
			}
		}
	}
	return ids
}

func buildInternalReport(jtbds *JTBDFile, scan *ScanResult, showUnmapped bool) *internalReport {
	// Initialize coverage map
	coverageMap := make(map[string]*Coverage)
	for id, job := range jtbds.Jobs {
		coverageMap[id] = &Coverage{
			JTBDID:    id,
			Principle: job.Principle,
			Statement: job.Statement,
			Keywords:  job.Keywords,
			TestFiles: []string{},
			Tests:     []TestInfo{},
		}
	}

	// Map test files to JTBDs with depth analysis
	mappedFiles := make(map[string]bool)
	for file, ids := range scan.fileMappings {
		mappedFiles[file] = true
		tests := scan.tests[file]

		for _, id := range ids {
			cov, ok := coverageMap[id]
			if !ok {
				continue
			}
			cov.TestFiles = append(cov.TestFiles, file)
			cov.Count++

			// Add individual tests and compute depth
			for _, test := range tests {
				cov.Tests = append(cov.Tests, test)
				switch test.Type {
				case TestTypeUnit:
					cov.UnitTests++
					cov.DepthScore++
				case TestTypeIntegration:
					cov.IntegrationTests++
					cov.DepthScore += 2
				case TestTypeE2E:
					cov.E2ETests++
					cov.DepthScore += 3
				}
			}
		}
	}

	// Build coverage slice sorted by JTBD ID
	coverage := make([]Coverage, 0, len(coverageMap))
	var coveredCount, totalDepthScore int
	for _, cov := range coverageMap {
		coverage = append(coverage, *cov)
		if cov.Count > 0 {
			coveredCount++
		}
		totalDepthScore += cov.DepthScore
	}
	sort.Slice(coverage, func(i, j int) bool {
		return coverage[i].JTBDID < coverage[j].JTBDID
	})

	// Find unmapped files
	var unmapped []string
	if showUnmapped {
		unmapped = scan.unmappedFiles
		sort.Strings(unmapped)
	}

	return &internalReport{
		TotalJTBDs:      len(jtbds.Jobs),
		CoveredJTBDs:    coveredCount,
		TotalTests:      len(scan.testFiles),
		MappedTests:     len(mappedFiles),
		TotalDepthScore: totalDepthScore,
		Coverage:        coverage,
		Unmapped:        unmapped,
	}
}

func buildValidation(scan *ScanResult, jtbds *JTBDFile) *ValidationResult {
	result := &ValidationResult{
		TotalAnnotations: len(scan.annotations),
	}

	for _, ann := range scan.annotations {
		if ann.Valid {
			result.ValidAnnotations++
		} else {
			if strings.Contains(ann.Reason, "not defined") {
				result.InvalidJTBDRefs = append(result.InvalidJTBDRefs, ann)
			} else {
				result.StaleAnnotations = append(result.StaleAnnotations, ann)
			}
		}
	}

	return result
}

func analyzeGaps(root string, jtbds *JTBDFile, coverage []Coverage) []GapAnalysis {
	var gaps []GapAnalysis

	// Find uncovered JTBDs
	for _, cov := range coverage {
		if cov.Count > 0 {
			continue
		}

		gap := GapAnalysis{
			JTBDID:    cov.JTBDID,
			Statement: cov.Statement,
			Keywords:  cov.Keywords,
		}

		if len(cov.Keywords) == 0 {
			gap.MissingKeywords = true
		} else {
			// Search for keyword matches in Go files
			implementedIn, matches := findKeywordMatches(root, cov.Keywords)
			gap.ImplementedIn = implementedIn
			gap.KeywordMatches = matches

			// Suggest test locations based on implementation files
			for _, impl := range implementedIn {
				dir := filepath.Dir(impl)
				testFile := strings.TrimSuffix(filepath.Base(impl), ".go") + "_test.go"
				suggested := filepath.Join(dir, testFile)
				if !contains(gap.SuggestedTestIn, suggested) {
					gap.SuggestedTestIn = append(gap.SuggestedTestIn, suggested)
				}
			}
		}

		gaps = append(gaps, gap)
	}

	return gaps
}

func findKeywordMatches(root string, keywords []string) ([]string, int) {
	var matchedFiles []string
	matchCount := 0
	seen := make(map[string]bool)

	// Build regex pattern from keywords
	patterns := make([]*regexp.Regexp, 0, len(keywords))
	for _, kw := range keywords {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
		patterns = append(patterns, pattern)
	}

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip test files, vendor, etc.
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || name == ".git" || name == ".codex" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check Go source files (not tests)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Check for keyword matches
		for _, pattern := range patterns {
			if pattern.Match(content) {
				matchCount++
				relPath, _ := filepath.Rel(root, path)
				if !seen[relPath] {
					seen[relPath] = true
					matchedFiles = append(matchedFiles, relPath)
				}
			}
		}

		return nil
	})

	// Sort by path and limit results
	sort.Strings(matchedFiles)
	if len(matchedFiles) > 5 {
		matchedFiles = matchedFiles[:5]
	}

	return matchedFiles, matchCount
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// buildCheckReport converts internal report to check.Report.
func buildCheckReport(r *internalReport) *check.Report {
	report := check.NewReport("jtbd")

	// Add metrics
	report.Metrics = []check.Metric{
		{Name: "total_jtbds", Value: float64(r.TotalJTBDs)},
		{Name: "covered_jtbds", Value: float64(r.CoveredJTBDs)},
		{Name: "total_tests", Value: float64(r.TotalTests)},
		{Name: "mapped_tests", Value: float64(r.MappedTests)},
		{Name: "depth_score", Value: float64(r.TotalDepthScore)},
	}

	// Add coverage percentage as a metric with threshold
	if r.TotalJTBDs > 0 {
		coveragePct := float64(r.CoveredJTBDs) * 100 / float64(r.TotalJTBDs)
		report.Metrics = append(report.Metrics, check.Metric{
			Name:      "coverage_pct",
			Value:     coveragePct,
			Unit:      "%",
			Threshold: 50,
			Op:        check.OpGTE,
		})
	}

	// Add uncovered JTBDs as items (errors)
	for _, cov := range r.Coverage {
		if cov.Count == 0 {
			report.Items = append(report.Items, check.Item{
				Severity: check.SeverityError,
				Label:    cov.JTBDID,
				Message:  truncate(cov.Statement, 60),
			})
		}
	}

	// Add validation errors if present
	if r.Validation != nil {
		for _, inv := range r.Validation.InvalidJTBDRefs {
			report.Items = append(report.Items, check.Item{
				Severity: check.SeverityWarn,
				Label:    fmt.Sprintf("%s:%d", inv.File, inv.Line),
				Message:  inv.Reason,
				Path:     inv.File,
			})
		}
	}

	// Add gap analysis as info items
	for _, gap := range r.Gaps {
		msg := "No coverage"
		if gap.MissingKeywords {
			msg = "No keywords defined"
		} else if len(gap.ImplementedIn) > 0 {
			msg = fmt.Sprintf("Likely in: %s", strings.Join(gap.ImplementedIn, ", "))
		}
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityInfo,
			Label:    gap.JTBDID,
			Message:  msg,
		})
	}

	// Add unmapped test files as info items
	for _, f := range r.Unmapped {
		report.Items = append(report.Items, check.Item{
			Severity: check.SeverityInfo,
			Label:    f,
			Message:  "unmapped test file",
			Path:     f,
		})
	}

	// Set summary
	if r.CoveredJTBDs == r.TotalJTBDs {
		report.Summary = fmt.Sprintf("All %d JTBDs covered", r.TotalJTBDs)
	} else {
		report.Summary = fmt.Sprintf("%d/%d JTBDs covered (%d%%)",
			r.CoveredJTBDs, r.TotalJTBDs,
			100*r.CoveredJTBDs/max(r.TotalJTBDs, 1))
	}

	return report
}

func outputCheck(r *internalReport) {
	report := buildCheckReport(r)
	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func outputHuman(r *internalReport) {
	report := buildCheckReport(r)
	cfg := check.DefaultHumanConfig()
	cfg.Purpose = "Map test coverage to user goals (Jobs To Be Done)"
	report.WriteHuman(os.Stdout, cfg)
}

func outputSARIF(r *internalReport) {
	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-jtbd"}},
	}

	// Add uncovered JTBDs as errors
	for _, cov := range r.Coverage {
		if cov.Count == 0 {
			run.Results = append(run.Results, sarif.Result{
				RuleID: "jtbd-uncovered",
				Level:  "error",
				Message: sarif.Message{
					Text: fmt.Sprintf("%s: %s", cov.JTBDID, truncate(cov.Statement, 80)),
				},
			})
		}
	}

	// Add validation issues
	if r.Validation != nil {
		for _, inv := range r.Validation.InvalidJTBDRefs {
			run.Results = append(run.Results, sarif.Result{
				RuleID: "jtbd-invalid-ref",
				Level:  "warning",
				Message: sarif.Message{
					Text: inv.Reason,
				},
				Locations: []sarif.Location{{
					PhysicalLocation: sarif.PhysicalLocation{
						ArtifactLocation: sarif.ArtifactLocation{URI: filepath.ToSlash(inv.File)},
						Region:           &sarif.Region{StartLine: inv.Line},
					},
				}},
			})
		}
	}

	log.Runs = append(log.Runs, run)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(log); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding SARIF: %v\n", err)
		os.Exit(1)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
