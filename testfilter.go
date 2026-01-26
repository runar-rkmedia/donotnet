package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TestFilter analyzes changed files to determine if we can run a subset of tests
type TestFilter struct {
	// ChangedFiles maps project path -> list of changed file paths (relative to git root)
	ChangedFiles map[string][]string
	// AllChangedFiles stores all changed files (relative to git root) for coverage lookup
	AllChangedFiles []string
	// CoverageMaps maps project name -> per-test coverage map (for coverage-based filtering)
	CoverageMaps map[string]*TestCoverageMap
}

// NewTestFilter creates a new TestFilter
func NewTestFilter() *TestFilter {
	return &TestFilter{
		ChangedFiles: make(map[string][]string),
	}
}

// AddChangedFile records a changed file for a project
func (tf *TestFilter) AddChangedFile(projectPath, filePath string) {
	tf.ChangedFiles[projectPath] = append(tf.ChangedFiles[projectPath], filePath)
	tf.AllChangedFiles = append(tf.AllChangedFiles, filePath)
}

// Clear resets the filter for a new run
func (tf *TestFilter) Clear() {
	tf.ChangedFiles = make(map[string][]string)
}

// FilterResult contains the result of analyzing changed files
type FilterResult struct {
	// CanFilter is true if we can safely filter to specific tests
	CanFilter bool
	// TestFilter is the dotnet test --filter expression (empty if CanFilter is false)
	TestFilter string
	// Reason explains why filtering is/isn't possible (for verbose output)
	Reason string
	// TestClasses lists the test classes that will be run
	TestClasses []string
}

// SetCoverageMaps sets the per-test coverage maps for coverage-based filtering
func (tf *TestFilter) SetCoverageMaps(maps map[string]*TestCoverageMap) {
	tf.CoverageMaps = maps
}

// GetFilter analyzes changed files for a project and returns a filter if possible
func (tf *TestFilter) GetFilter(projectPath string, gitRoot string) FilterResult {
	// First, try coverage-based filtering using all changed files
	// This works even when the changed file is in a different project than the test project
	if len(tf.AllChangedFiles) > 0 && len(tf.CoverageMaps) > 0 {
		result := tf.getFilterWithCoverage(projectPath, tf.AllChangedFiles, nil)
		if result.CanFilter {
			return result
		}
		// If coverage lookup found no tests or file not covered, continue to heuristics
	}

	// Fall back to heuristic-based filtering using per-project changed files
	files, ok := tf.ChangedFiles[projectPath]
	if !ok || len(files) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "no changed files tracked for this project",
		}
	}

	// Check each changed file
	var testClasses []string
	var nonTestFiles []string
	for _, file := range files {
		class, isTest := tf.analyzeFile(file, gitRoot)
		if isTest {
			if class != "" {
				testClasses = append(testClasses, class)
			}
		} else {
			nonTestFiles = append(nonTestFiles, file)
		}
	}

	// If we have non-test files and no coverage data, can't filter
	if len(nonTestFiles) > 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    fmt.Sprintf("non-test file changed: %s", filepath.Base(nonTestFiles[0])),
		}
	}

	if len(testClasses) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "no test classes identified",
		}
	}

	// Remove duplicates
	testClasses = uniqueStrings(testClasses)

	// Build filter expression
	// Using FullyQualifiedName~ for partial match (handles namespaces)
	var filterParts []string
	for _, class := range testClasses {
		filterParts = append(filterParts, fmt.Sprintf("FullyQualifiedName~%s", class))
	}
	filter := strings.Join(filterParts, "|")

	return FilterResult{
		CanFilter:   true,
		TestFilter:  filter,
		Reason:      fmt.Sprintf("only test files changed: %s", strings.Join(testClasses, ", ")),
		TestClasses: testClasses,
	}
}

// getFilterWithCoverage attempts to build a filter using per-test coverage data
func (tf *TestFilter) getFilterWithCoverage(projectPath string, changedFiles []string, testClasses []string) FilterResult {
	// Get project name from path (e.g., "tests/Foo.Tests/Foo.Tests.csproj" -> "Foo.Tests")
	projectName := strings.TrimSuffix(filepath.Base(projectPath), ".csproj")

	covMap, ok := tf.CoverageMaps[projectName]
	if !ok || covMap == nil {
		return FilterResult{
			CanFilter: false,
			Reason:    fmt.Sprintf("no coverage data for %s", projectName),
		}
	}

	// Collect tests that cover any of the changed files
	testsToRun := make(map[string]bool)
	var uncoveredSourceFiles []string

	for _, file := range changedFiles {
		// Normalize path separators
		normalizedFile := filepath.ToSlash(file)

		// Check if this is a test file - if so, add the class name directly
		fileName := filepath.Base(file)
		nameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		if strings.HasSuffix(nameWithoutExt, "Tests") || strings.HasSuffix(nameWithoutExt, "Test") {
			testsToRun[nameWithoutExt] = true
			continue
		}

		// Look up in coverage map
		tests, covered := covMap.FileToTests[normalizedFile]
		if covered && len(tests) > 0 {
			for _, t := range tests {
				testsToRun[t] = true
			}
		} else {
			// Only track as uncovered if it's a .cs file (not .csproj, .razor, etc.)
			if strings.HasSuffix(strings.ToLower(file), ".cs") {
				uncoveredSourceFiles = append(uncoveredSourceFiles, filepath.Base(file))
			}
		}
	}

	// If any source files are not in coverage data, fall back to running all tests
	if len(uncoveredSourceFiles) > 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    fmt.Sprintf("file(s) not in coverage: %s", strings.Join(uncoveredSourceFiles, ", ")),
		}
	}

	// Add test classes from test file changes (passed in separately)
	for _, class := range testClasses {
		testsToRun[class] = true
	}

	if len(testsToRun) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "no tests cover the changed files",
		}
	}

	// Build filter expression using FullyQualifiedName=
	var filterParts []string
	var testNames []string
	for test := range testsToRun {
		testNames = append(testNames, test)
		filterParts = append(filterParts, fmt.Sprintf("FullyQualifiedName~%s", test))
	}
	filter := strings.Join(filterParts, "|")

	return FilterResult{
		CanFilter:   true,
		TestFilter:  filter,
		Reason:      fmt.Sprintf("coverage-based: %d test(s) for %d file(s)", len(testsToRun), len(changedFiles)),
		TestClasses: testNames,
	}
}

// analyzeFile checks if a file is a test file and returns the test class name
// Returns (className, isTestFile) - if isTestFile is false, we can't filter
func (tf *TestFilter) analyzeFile(filePath string, gitRoot string) (string, bool) {
	fileName := filepath.Base(filePath)
	ext := strings.ToLower(filepath.Ext(fileName))

	// Only analyze .cs files
	if ext != ".cs" {
		// Non-.cs files (like .csproj, .razor) mean we can't filter
		return "", false
	}

	nameWithoutExt := strings.TrimSuffix(fileName, ext)

	// Case 1: File ends with Tests.cs or Test.cs - this is a test file
	if strings.HasSuffix(nameWithoutExt, "Tests") || strings.HasSuffix(nameWithoutExt, "Test") {
		// The class name is typically the filename without extension
		return nameWithoutExt, true
	}

	// Case 2: Check if the file contains only test classes by reading it
	// This handles cases where test files don't follow the naming convention
	fullPath := filepath.Join(gitRoot, filePath)
	if isTestOnlyFile(fullPath) {
		return nameWithoutExt, true
	}

	// This is not a test file - can't filter
	return "", false
}

// Regex to find test attributes in C# code
var testAttributeRegex = regexp.MustCompile(`\[(Test|Fact|Theory|TestMethod|TestCase)\b`)
var classRegex = regexp.MustCompile(`\bclass\s+(\w+)`)

// isTestOnlyFile checks if a file contains only test classes (has test attributes)
// This is a heuristic - if the file has test attributes, we assume it's a test file
func isTestOnlyFile(filePath string) bool {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	// If the file has test attributes, consider it a test file
	return testAttributeRegex.Match(content)
}

// extractTestClassName tries to extract the test class name from file content
// Returns empty string if no class found
func extractTestClassName(filePath string) string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	matches := classRegex.FindStringSubmatch(string(content))
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func uniqueStrings(input []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
