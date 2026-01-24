// Command html-hygiene validates exported HTML for broken links and structural integrity.
//
// Checks:
//   - Internal links resolve to existing files
//   - Tag links are properly sanitized (colons to dashes)
//   - JS-heavy pages have required content markers
//   - No stale files from deleted content
//   - Template rendering (no empty content, unrendered variables)
//   - Critical pages have expected structure
//
// Usage:
//
//	html-hygiene                         # validate ~/Projects/kg
//	html-hygiene -root=/path/to/export   # validate specific directory
//	html-hygiene -format=check           # lintkit-check JSON
//	html-hygiene -format=sarif           # SARIF JSON
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dkoosis/lintkit/pkg/check"
	"github.com/dkoosis/lintkit/pkg/sarif"
)

const (
	severityError   = "error"
	severityWarning = "warning"
	severityInfo    = "info"
	indexHTMLFile   = "index.html"
)

// Issue categories
const (
	catBrokenLink         = "broken-link"          // Generic broken link
	catServerEndpoint     = "server-endpoint"      // /admin/* links that need running server
	catStaleFile          = "stale-file"           // HTML file with no corresponding nugget
	catTagNotSanitized    = "tag-not-sanitized"    // Tag link with colons (should be dashes)
	catKindMismatch       = "kind-mismatch"        // Link kind doesn't match export path
	catJSTemplateLiteral  = "js-template-literal"  // ${...} in link (JS, not broken)
	catMissingPage        = "missing-page"         // Expected page not found
	catJSPageContent      = "js-page-content"      // JS page missing required markers
	catBrokenProjectLink  = "broken-project-link"  // Project index link broken
	catEmptyProjectIndex  = "empty-project-index"  // No projects in index
	catExternalInContent  = "external-in-content"  // External URL parsed as link
	catEmptyContent       = "empty-content"        // Empty body section in HTML
	catMissingMetadata    = "missing-metadata"     // Missing title/tags/breadcrumbs
	catUnrenderedTemplate = "unrendered-template"  // {{.Field}} in output
	catIndexIncomplete    = "index-incomplete"     // Index page missing expected content
)

// Issue represents a validation finding.
type Issue struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

var (
	format       = flag.String("format", "human", "output format: sarif, check, human")
	exportRoot   = flag.String("root", "", "HTML export directory (default: ~/Projects/kg)")
	strict       = flag.Bool("strict", false, "treat warnings as errors")
	minKindLinks = flag.Int("min-kind-links", 3, "minimum kind links required on index.html")
)

// Patterns for extracting links from HTML
var (
	hrefPattern           = regexp.MustCompile(`href="([^"#?]+)"`)
	srcPattern            = regexp.MustCompile(`src="([^"#?]+)"`)
	jsTemplatePattern     = regexp.MustCompile(`\$\{[^}]+\}`)
	unrenderedTmplPattern = regexp.MustCompile(`\{\{\.?\w+(\.\w+)*\}\}`)
	titlePattern          = regexp.MustCompile(`<title>([^<]*)</title>`)
	h1Pattern             = regexp.MustCompile(`<h1[^>]*>([^<]*)</h1>`)
	contentPattern        = regexp.MustCompile(`(?s)<main[^>]*>(.*?)</main>|<article[^>]*>(.*?)</article>|<div[^>]*class="[^"]*content[^"]*"[^>]*>(.*?)</div>`)
)

// Export routing rules - kinds that map to different directories
var kindRouting = map[string]string{
	"prompt": "ref", // prompt nuggets export to /ref/
}

// Server endpoints - these are valid but only work with running server
var serverEndpoints = []string{
	"/admin/",
	"/api/",
	"/health",
	"/metrics",
}

// Valid top-level export directories
var validKindDirs = map[string]bool{
	"adr": true, "check": true, "cite": true, "doc": true,
	"entity": true, "event": true, "journal": true, "log": true,
	"map": true, "org": true, "pattern": true, "person": true,
	"place": true, "plan": true, "project": true, "ref": true,
	"rule": true, "schema": true, "spark": true, "system": true,
	"task": true, "tags": true, "tool": true, "trap": true,
	"people": true, "static": true, "data": true, "choice": true,
}

// Key JS pages that need validation
var jsPages = []struct {
	path    string
	name    string
	markers []string
}{
	{
		path:    "system/tools/graph.html",
		name:    "Graph Viewer",
		markers: []string{"cytoscape", "graph-container"},
	},
	{
		path:    "system/tools/tool-stats.html",
		name:    "Tool Stats (Sankey)",
		markers: []string{"d3", "sankey", "svg"},
	},
	{
		path:    "system/tools/metrics-dashboard.html",
		name:    "Metrics Dashboard",
		markers: []string{"chart", "metrics"},
	},
}

func main() {
	flag.Parse()

	root := *exportRoot
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "Projects", "kg")
	}

	if _, err := os.Stat(root); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Export directory not found: %s\n", root)
		fmt.Fprintf(os.Stderr, "Specify a valid directory with -root flag\n")
		os.Exit(1)
	}

	issues := validate(root)

	switch *format {
	case "sarif":
		outputSARIF(issues)
	case "check":
		outputCheck(issues)
	default:
		outputHuman(issues)
	}

	// Exit with error if any errors found (or warnings in strict mode)
	for _, issue := range issues {
		if issue.Severity == severityError {
			os.Exit(1)
		}
		if *strict && issue.Severity == severityWarning {
			os.Exit(1)
		}
	}
}

func validate(root string) []Issue {
	var issues []Issue

	// Build index of all files
	htmlFiles := make(map[string]bool)
	staticFiles := make(map[string]bool)
	linkedFiles := make(map[string]bool) // Track which files are linked to

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		ext := strings.ToLower(filepath.Ext(path))

		switch ext {
		case ".html":
			htmlFiles[relPath] = true
		case ".css", ".js", ".json", ".png", ".jpg", ".svg", ".woff", ".woff2", ".db", ".db-shm", ".db-wal", ".jsonl":
			staticFiles[relPath] = true
		}
		return nil
	})

	// Validate links and track what's linked
	linkIssues, linked := validateLinks(root, htmlFiles, staticFiles)
	issues = append(issues, linkIssues...)
	for f := range linked {
		linkedFiles[f] = true
	}

	// Check for stale files (exist but not linked and not index pages)
	issues = append(issues, detectStaleFiles(htmlFiles, linkedFiles)...)

	// Validate JS pages
	issues = append(issues, validateJSPages(root)...)

	// Validate project structure
	issues = append(issues, validateProjectLinks(root, htmlFiles)...)

	// Validate template rendering
	issues = append(issues, validateTemplateRendering(root, htmlFiles)...)

	// Validate critical pages
	issues = append(issues, validateCriticalPages(root, htmlFiles)...)

	return issues
}

func validateLinks(root string, htmlFiles, staticFiles map[string]bool) ([]Issue, map[string]bool) {
	var issues []Issue
	linkedFiles := make(map[string]bool)

	for htmlFile := range htmlFiles {
		fullPath := filepath.Join(root, htmlFile)
		content, err := os.ReadFile(fullPath) //nolint:gosec
		if err != nil {
			continue
		}

		dir := filepath.Dir(htmlFile)
		contentStr := string(content)

		// Extract and validate all links
		allMatches := hrefPattern.FindAllStringSubmatch(contentStr, -1)
		allMatches = append(allMatches, srcPattern.FindAllStringSubmatch(contentStr, -1)...)

		for _, match := range allMatches {
			link := match[1]
			issue, targetPath := checkLink(htmlFile, link, dir, root, htmlFiles, staticFiles)
			if targetPath != "" {
				linkedFiles[targetPath] = true
			}
			if issue != nil {
				issues = append(issues, *issue)
			}
		}
	}

	return issues, linkedFiles
}

func checkLink(sourcePath, link, sourceDir, root string, htmlFiles, staticFiles map[string]bool) (*Issue, string) {
	// Skip external links
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") ||
		strings.HasPrefix(link, "mailto:") || strings.HasPrefix(link, "javascript:") ||
		strings.HasPrefix(link, "data:") {
		return nil, ""
	}

	// Skip empty or anchor-only links
	if link == "" || link == "#" {
		return nil, ""
	}

	// Check for JS template literals (not actual broken links)
	if jsTemplatePattern.MatchString(link) {
		return &Issue{
			Severity: severityInfo,
			Category: catJSTemplateLiteral,
			Path:     sourcePath,
			Message:  fmt.Sprintf("JavaScript template literal in link: %s", link),
			Fix:      "This is generated dynamically in JS; not a broken link",
		}, ""
	}

	// Check for server endpoints
	for _, endpoint := range serverEndpoints {
		if strings.HasPrefix(link, endpoint) {
			return &Issue{
				Severity: severityInfo,
				Category: catServerEndpoint,
				Path:     sourcePath,
				Message:  fmt.Sprintf("Server endpoint (requires running server): %s", link),
				Fix:      "This link only works when server is running",
			}, ""
		}
	}

	// Check for unsanitized tag links (colons in tag filenames)
	if strings.Contains(link, "/tags/") && strings.Contains(link, ":") {
		return &Issue{
			Severity: severityError,
			Category: catTagNotSanitized,
			Path:     sourcePath,
			Message:  fmt.Sprintf("Tag link not sanitized (contains colon): %s", link),
			Fix:      "Use tagSlug filter in template: {{.Tag | tagSlug}}",
		}, ""
	}

	// Check for external URLs that got parsed as relative (common in content)
	if strings.Contains(link, ".com/") || strings.Contains(link, ".org/") ||
		strings.Contains(link, ".gov/") || strings.Contains(link, ".io/") {
		return &Issue{
			Severity: severityInfo,
			Category: catExternalInContent,
			Path:     sourcePath,
			Message:  fmt.Sprintf("External URL in content parsed as link: %s", link),
			Fix:      "This appears to be content, not a navigation link",
		}, ""
	}

	// Resolve relative path
	var targetPath string
	if strings.HasPrefix(link, "/") {
		targetPath = strings.TrimPrefix(link, "/")
	} else if strings.HasPrefix(link, "./") {
		targetPath = filepath.Join(sourceDir, strings.TrimPrefix(link, "./"))
	} else if strings.HasPrefix(link, "../") {
		targetPath = filepath.Join(sourceDir, link)
	} else {
		targetPath = filepath.Join(sourceDir, link)
	}

	targetPath = filepath.Clean(targetPath)

	// Check if target exists
	if htmlFiles[targetPath] || staticFiles[targetPath] {
		return nil, targetPath
	}

	// Check if file actually exists on disk
	fullTarget := filepath.Join(root, targetPath)
	if _, err := os.Stat(fullTarget); err == nil {
		return nil, targetPath
	}

	// Check for kind routing mismatch
	if issue := checkKindMismatch(sourcePath, link, targetPath); issue != nil {
		return issue, ""
	}

	return &Issue{
		Severity: severityError,
		Category: catBrokenLink,
		Path:     sourcePath,
		Message:  fmt.Sprintf("Broken link: %s -> %s", link, targetPath),
		Fix:      "Fix the link target or create the missing file",
	}, ""
}

// checkKindMismatch detects when a link uses a kind that should route differently
func checkKindMismatch(sourcePath, link, targetPath string) *Issue {
	parts := strings.Split(targetPath, "/")
	if len(parts) < 2 {
		return nil
	}

	linkKind := parts[0]

	// Check if this kind should route to a different directory
	if expectedDir, ok := kindRouting[linkKind]; ok {
		return &Issue{
			Severity: severityError,
			Category: catKindMismatch,
			Path:     sourcePath,
			Message:  fmt.Sprintf("Kind mismatch: %s links to /%s/ but should use /%s/", link, linkKind, expectedDir),
			Fix:      fmt.Sprintf("Update link to use /%s/ instead of /%s/", expectedDir, linkKind),
		}
	}

	return nil
}

// detectStaleFiles finds HTML files that exist but aren't linked and aren't expected
func detectStaleFiles(htmlFiles, linkedFiles map[string]bool) []Issue {
	var issues []Issue

	// Files that are expected even if not linked
	expectedFiles := map[string]bool{
		indexHTMLFile: true,
	}

	// Index files in any directory are expected
	isIndexFile := func(path string) bool {
		return strings.HasSuffix(path, "/"+indexHTMLFile) || path == indexHTMLFile
	}

	// System files are expected
	isSystemFile := func(path string) bool {
		return strings.HasPrefix(path, "system/") ||
			strings.HasPrefix(path, "static/") ||
			strings.HasPrefix(path, "data/")
	}

	for file := range htmlFiles {
		if expectedFiles[file] || isIndexFile(file) || isSystemFile(file) || linkedFiles[file] {
			continue
		}

		// Check if this is in a valid kind directory
		parts := strings.Split(file, "/")
		if len(parts) >= 2 {
			kindDir := parts[0]
			if !validKindDirs[kindDir] {
				issues = append(issues, Issue{
					Severity: severityWarning,
					Category: catStaleFile,
					Path:     file,
					Message:  fmt.Sprintf("File in unknown directory: %s", kindDir),
					Fix:      "This may be a stale file from old export; consider removing",
				})
			}
		}
	}

	return issues
}

func validateJSPages(root string) []Issue {
	var issues []Issue

	for _, page := range jsPages {
		fullPath := filepath.Join(root, page.path)
		content, err := os.ReadFile(fullPath) //nolint:gosec
		if os.IsNotExist(err) {
			issues = append(issues, Issue{
				Severity: severityWarning,
				Category: catMissingPage,
				Path:     page.path,
				Message:  fmt.Sprintf("%s page not found", page.name),
				Fix:      "Regenerate HTML export to create this page",
			})
			continue
		}
		if err != nil {
			continue
		}

		contentStr := strings.ToLower(string(content))
		var missingMarkers []string
		for _, marker := range page.markers {
			if !strings.Contains(contentStr, strings.ToLower(marker)) {
				missingMarkers = append(missingMarkers, marker)
			}
		}

		if len(missingMarkers) > 0 {
			issues = append(issues, Issue{
				Severity: severityWarning,
				Category: catJSPageContent,
				Path:     page.path,
				Message:  fmt.Sprintf("%s missing expected content: %s", page.name, strings.Join(missingMarkers, ", ")),
				Fix:      "Page may not render correctly; check data export",
			})
		}
	}

	return issues
}

func validateProjectLinks(root string, htmlFiles map[string]bool) []Issue {
	var issues []Issue

	projectIndex := "project/index.html"
	if !htmlFiles[projectIndex] {
		issues = append(issues, Issue{
			Severity: severityWarning,
			Category: catMissingPage,
			Path:     projectIndex,
			Message:  "Project index page not found",
			Fix:      "Export may have failed or no project nuggets exist",
		})
		return issues
	}

	fullPath := filepath.Join(root, projectIndex)
	content, err := os.ReadFile(fullPath) //nolint:gosec
	if err != nil {
		return issues
	}

	contentStr := string(content)
	projectLinks := hrefPattern.FindAllStringSubmatch(contentStr, -1)
	projectLinksFound := 0

	for _, match := range projectLinks {
		link := match[1]
		if !strings.Contains(link, "project/") && !strings.Contains(link, "task/") &&
			!strings.Contains(link, "spark/") {
			continue
		}
		if link == "index.html" || strings.HasSuffix(link, "/index.html") {
			continue
		}

		projectLinksFound++

		// Resolve the link relative to project/
		var targetPath string
		if strings.HasPrefix(link, "../") {
			targetPath = filepath.Clean(filepath.Join("project", link))
		} else if strings.HasPrefix(link, "./") {
			targetPath = filepath.Clean(filepath.Join("project", strings.TrimPrefix(link, "./")))
		} else {
			targetPath = filepath.Clean(filepath.Join("project", link))
		}

		if !htmlFiles[targetPath] {
			// Check if it's actually at the root level (task/, spark/)
			rootPath := filepath.Clean(strings.TrimPrefix(link, "../"))
			if !htmlFiles[rootPath] {
				issues = append(issues, Issue{
					Severity: severityError,
					Category: catBrokenProjectLink,
					Path:     projectIndex,
					Message:  fmt.Sprintf("Project link broken: %s (tried %s and %s)", link, targetPath, rootPath),
					Fix:      "Project page not exported; check export logic",
				})
			}
		}
	}

	if projectLinksFound == 0 {
		issues = append(issues, Issue{
			Severity: severityInfo,
			Category: catEmptyProjectIndex,
			Path:     projectIndex,
			Message:  "Project index has no project links",
			Fix:      "Check if project nuggets exist in the knowledge graph",
		})
	}

	return issues
}

// validateTemplateRendering checks for common template rendering failures
func validateTemplateRendering(root string, htmlFiles map[string]bool) []Issue {
	var issues []Issue

	for htmlFile := range htmlFiles {
		// Skip system/static files
		if strings.HasPrefix(htmlFile, "system/") ||
			strings.HasPrefix(htmlFile, "static/") ||
			strings.HasPrefix(htmlFile, "data/") {
			continue
		}

		fullPath := filepath.Join(root, htmlFile)
		content, err := os.ReadFile(fullPath) //nolint:gosec
		if err != nil {
			continue
		}
		contentStr := string(content)

		// Check for unrendered template variables
		if matches := unrenderedTmplPattern.FindAllString(contentStr, -1); len(matches) > 0 {
			// Filter out common false positives (JS frameworks, Vue, etc.)
			var realMatches []string
			for _, m := range matches {
				// Skip common JS framework patterns
				if strings.Contains(m, "{{#") || strings.Contains(m, "{{/") ||
					strings.Contains(m, "{{>") || strings.Contains(m, "{{^") {
					continue
				}
				realMatches = append(realMatches, m)
			}
			if len(realMatches) > 0 {
				maxShow := 3
				if len(realMatches) < maxShow {
					maxShow = len(realMatches)
				}
				issues = append(issues, Issue{
					Severity: severityWarning, // warning: often content contains template examples
					Category: catUnrenderedTemplate,
					Path:     htmlFile,
					Message:  fmt.Sprintf("Unrendered template variables: %s", strings.Join(realMatches[:maxShow], ", ")),
					Fix:      "Template rendering failed; check template data",
				})
			}
		}

		// Check for empty content (but only for non-index pages)
		if !isExpectedWithoutNugget(htmlFile) && !strings.HasSuffix(htmlFile, "/index.html") {
			// Look for main content area
			contentMatches := contentPattern.FindStringSubmatch(contentStr)
			hasContent := false
			for _, match := range contentMatches {
				if strings.TrimSpace(match) != "" {
					// Check if content has actual text (not just HTML tags)
					text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(match, "")
					if len(strings.TrimSpace(text)) > 10 {
						hasContent = true
						break
					}
				}
			}

			// Also check for article content
			if !hasContent && strings.Contains(contentStr, "<article") {
				articlePattern := regexp.MustCompile(`(?s)<article[^>]*>(.*?)</article>`)
				if articleMatch := articlePattern.FindStringSubmatch(contentStr); len(articleMatch) > 1 {
					text := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(articleMatch[1], "")
					if len(strings.TrimSpace(text)) > 10 {
						hasContent = true
					}
				}
			}

			// Only flag if file is large enough to have had content
			if !hasContent && len(content) > 500 {
				issues = append(issues, Issue{
					Severity: severityWarning,
					Category: catEmptyContent,
					Path:     htmlFile,
					Message:  "Page appears to have empty or minimal content",
					Fix:      "Check if nugget body is empty or template has rendering issues",
				})
			}
		}

		// Check for missing title
		if !titlePattern.MatchString(contentStr) && !h1Pattern.MatchString(contentStr) {
			issues = append(issues, Issue{
				Severity: severityWarning,
				Category: catMissingMetadata,
				Path:     htmlFile,
				Message:  "Page missing title element",
				Fix:      "Add <title> tag or check template rendering",
			})
		}
	}

	return issues
}

// isExpectedWithoutNugget returns true for HTML files that don't map to nuggets
func isExpectedWithoutNugget(path string) bool {
	// Index pages
	if strings.HasSuffix(path, "/index.html") || path == "index.html" {
		return true
	}
	// System/static/data directories
	if strings.HasPrefix(path, "system/") ||
		strings.HasPrefix(path, "static/") ||
		strings.HasPrefix(path, "data/") {
		return true
	}
	// Tag pages
	if strings.HasPrefix(path, "tags/") {
		return true
	}
	// Schema pages
	if strings.HasPrefix(path, "schema/") {
		return true
	}
	// Journal pages
	if strings.HasPrefix(path, "journal/") {
		return true
	}
	// People section pages
	if strings.HasPrefix(path, "people/") {
		return true
	}
	return false
}

// validateCriticalPages checks index.html and kind index pages for completeness
func validateCriticalPages(root string, htmlFiles map[string]bool) []Issue {
	var issues []Issue

	// Validate main index.html
	indexPath := filepath.Join(root, "index.html")
	if content, err := os.ReadFile(indexPath); err == nil { //nolint:gosec
		contentStr := string(content)

		// Count kind links
		kindLinks := 0
		for kind := range validKindDirs {
			if strings.Contains(contentStr, fmt.Sprintf("/%s/", kind)) ||
				strings.Contains(contentStr, fmt.Sprintf("href=\"%s/", kind)) {
				kindLinks++
			}
		}

		if kindLinks < *minKindLinks {
			issues = append(issues, Issue{
				Severity: severityWarning,
				Category: catIndexIncomplete,
				Path:     "index.html",
				Message:  fmt.Sprintf("Main index has only %d kind links (expected at least %d)", kindLinks, *minKindLinks),
				Fix:      "Check if export is complete or if nuggets exist",
			})
		}
	}

	return issues
}

// --- Output functions ---

func outputSARIF(issues []Issue) {
	log := sarif.NewLog()
	run := sarif.Run{
		Tool: sarif.Tool{Driver: sarif.Driver{Name: "lintkit-html-hygiene"}},
	}

	for _, issue := range issues {
		level := "note"
		switch issue.Severity {
		case severityError:
			level = "error"
		case severityWarning:
			level = "warning"
		}

		result := sarif.Result{
			RuleID:  issue.Category,
			Level:   level,
			Message: sarif.Message{Text: issue.Message},
		}

		if issue.Path != "" {
			result.Locations = []sarif.Location{{
				PhysicalLocation: sarif.PhysicalLocation{
					ArtifactLocation: sarif.ArtifactLocation{URI: filepath.ToSlash(issue.Path)},
				},
			}}
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

func outputCheck(issues []Issue) {
	report := check.NewReport("html-hygiene")

	// Count by severity
	errors, warnings, infos := 0, 0, 0
	for _, issue := range issues {
		switch issue.Severity {
		case severityError:
			errors++
		case severityWarning:
			warnings++
		case severityInfo:
			infos++
		}
	}

	// Add metrics
	report.Metrics = []check.Metric{
		{Name: "errors", Value: float64(errors), Threshold: 0, Op: check.OpLTE},
		{Name: "warnings", Value: float64(warnings)},
		{Name: "info", Value: float64(infos)},
	}

	// Add items for issues
	for _, issue := range issues {
		var sev check.Severity
		switch issue.Severity {
		case severityError:
			sev = check.SeverityError
		case severityWarning:
			sev = check.SeverityWarn
		default:
			sev = check.SeverityInfo
		}

		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    fmt.Sprintf("[%s]", issue.Category),
			Path:     issue.Path,
			Message:  issue.Message,
			Fix:      issue.Fix,
		})
	}

	// Set summary
	if errors == 0 && warnings == 0 {
		report.Summary = "HTML export validation passed"
	} else {
		var parts []string
		if errors > 0 {
			parts = append(parts, fmt.Sprintf("%d errors", errors))
		}
		if warnings > 0 {
			parts = append(parts, fmt.Sprintf("%d warnings", warnings))
		}
		if infos > 0 {
			parts = append(parts, fmt.Sprintf("%d info", infos))
		}
		report.Summary = strings.Join(parts, ", ")
	}

	if err := report.Write(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error encoding check output: %v\n", err)
		os.Exit(1)
	}
}

func outputHuman(issues []Issue) {
	report := check.NewReport("html-hygiene")

	// Count by severity
	errors, warnings, infos := 0, 0, 0
	for _, issue := range issues {
		switch issue.Severity {
		case severityError:
			errors++
		case severityWarning:
			warnings++
		case severityInfo:
			infos++
		}
	}

	// Group by category for cleaner output
	byCategory := make(map[string][]Issue)
	for _, issue := range issues {
		byCategory[issue.Category] = append(byCategory[issue.Category], issue)
	}

	categories := make([]string, 0, len(byCategory))
	for cat := range byCategory {
		categories = append(categories, cat)
	}
	sort.Strings(categories)

	// Add category summaries first
	for _, cat := range categories {
		catIssues := byCategory[cat]
		var sev check.Severity
		switch catIssues[0].Severity {
		case severityError:
			sev = check.SeverityError
		case severityWarning:
			sev = check.SeverityWarn
		default:
			sev = check.SeverityInfo
		}

		report.Items = append(report.Items, check.Item{
			Severity: sev,
			Label:    fmt.Sprintf("[%s]", cat),
			Value:    fmt.Sprintf("%d issues", len(catIssues)),
		})

		// Show first few examples
		maxShow := 3
		if len(catIssues) < maxShow {
			maxShow = len(catIssues)
		}
		for i := 0; i < maxShow; i++ {
			issue := catIssues[i]
			report.Items = append(report.Items, check.Item{
				Severity: sev,
				Label:    "  " + issue.Path,
				Message:  issue.Message,
			})
		}
		if len(catIssues) > maxShow {
			report.Items = append(report.Items, check.Item{
				Severity: check.SeverityInfo,
				Label:    fmt.Sprintf("  ... and %d more", len(catIssues)-maxShow),
			})
		}
	}

	// Metrics
	report.Metrics = []check.Metric{
		{Name: "errors", Value: float64(errors)},
		{Name: "warnings", Value: float64(warnings)},
		{Name: "info", Value: float64(infos)},
	}

	// Summary
	if errors == 0 && warnings == 0 {
		report.Summary = "HTML export validation passed"
		report.Status = check.StatusPass
	} else if errors > 0 {
		report.Summary = fmt.Sprintf("%d errors, %d warnings, %d info", errors, warnings, infos)
		report.Status = check.StatusFail
	} else {
		report.Summary = fmt.Sprintf("%d warnings, %d info", warnings, infos)
		report.Status = check.StatusWarn
	}

	cfg := check.DefaultHumanConfig()
	cfg.Purpose = "Validate HTML exports for broken links and structural integrity"
	report.WriteHuman(os.Stdout, cfg)
}
