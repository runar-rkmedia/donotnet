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
	// Heuristics is the list of enabled heuristics for fallback filtering
	Heuristics []TestHeuristic
}

// NewTestFilter creates a new TestFilter with default heuristics enabled
func NewTestFilter() *TestFilter {
	return &TestFilter{
		ChangedFiles: make(map[string][]string),
		Heuristics:   AvailableHeuristics, // All enabled by default
	}
}

// SetHeuristics sets the enabled heuristics for fallback filtering
func (tf *TestFilter) SetHeuristics(heuristics []TestHeuristic) {
	tf.Heuristics = heuristics
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
		// If coverage lookup found no tests or file not covered, try heuristics
	}

	// Try heuristic-based filtering using all changed files
	if len(tf.AllChangedFiles) > 0 {
		result := tf.getFilterWithHeuristics(tf.AllChangedFiles, gitRoot)
		if result.CanFilter {
			return result
		}
	}

	// Final fallback for test-file-only changes (legacy behavior)
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

	// If we have non-test files, can't filter without coverage or heuristics
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

// getFilterWithHeuristics uses naming conventions to guess which tests to run
// Based on analysis: ~79% of files follow Foo.cs -> FooTests pattern
func (tf *TestFilter) getFilterWithHeuristics(changedFiles []string, gitRoot string) FilterResult {
	// If no heuristics are enabled, return early
	if len(tf.Heuristics) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "heuristics disabled",
		}
	}

	testsToRun := make(map[string]bool)
	var nonCsFiles []string
	var usedHeuristics []string

	for _, file := range changedFiles {
		fileName := filepath.Base(file)
		ext := strings.ToLower(filepath.Ext(fileName))

		// Skip non-.cs files - they could be .csproj, .razor, etc.
		if ext != ".cs" {
			nonCsFiles = append(nonCsFiles, fileName)
			continue
		}

		nameWithoutExt := strings.TrimSuffix(fileName, ext)

		// If this is already a test file, add it directly
		if strings.HasSuffix(nameWithoutExt, "Tests") || strings.HasSuffix(nameWithoutExt, "Test") {
			testsToRun[nameWithoutExt] = true
			continue
		}

		// Check if this file contains test attributes (might be a test file with non-standard name)
		fullPath := filepath.Join(gitRoot, file)
		if isTestOnlyFile(fullPath) {
			testsToRun[nameWithoutExt] = true
			continue
		}

		// Get parent directory name for heuristics
		dirName := ""
		dir := filepath.Dir(file)
		if dir != "." {
			parts := strings.Split(filepath.ToSlash(dir), "/")
			for i := len(parts) - 1; i >= 0; i-- {
				d := parts[i]
				if d != "" && d != "Source" && d != "src" && !strings.HasSuffix(d, ".csproj") {
					dirName = d
					break
				}
			}
		}

		// Apply each enabled heuristic
		for _, h := range tf.Heuristics {
			patterns := h.Apply(nameWithoutExt, dirName)
			for _, p := range patterns {
				if p != "" {
					testsToRun[p] = true
					// Track which heuristics were used (for verbose output)
					found := false
					for _, u := range usedHeuristics {
						if u == h.Name {
							found = true
							break
						}
					}
					if !found {
						usedHeuristics = append(usedHeuristics, h.Name)
					}
				}
			}
		}
	}

	// If there are non-.cs files, we can't safely use heuristics
	if len(nonCsFiles) > 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    fmt.Sprintf("non-.cs file changed: %s", nonCsFiles[0]),
		}
	}

	if len(testsToRun) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "no heuristic matches found",
		}
	}

	// Build filter expression
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
		Reason:      fmt.Sprintf("heuristic [%s]: %d pattern(s) for %d file(s)", strings.Join(usedHeuristics, ","), len(testsToRun), len(changedFiles)),
		TestClasses: testNames,
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

// TestHeuristic defines a named heuristic for guessing test names from source files
type TestHeuristic struct {
	Name        string // Short identifier (e.g., "NameToNameTests")
	Description string // Human-readable description
	// Apply returns test patterns to match for a given source file
	// fileName is without extension, dirName is the immediate parent directory
	Apply func(fileName, dirName string) []string
}

// AvailableHeuristics lists all available test-filtering heuristics
// The first two are enabled by default, the rest are opt-in
var AvailableHeuristics = []TestHeuristic{
	// Default heuristics (enabled with "all")
	{
		Name:        "NameToNameTests",
		Description: "Foo.cs -> FooTests (direct name match, ~79% accurate)",
		Apply: func(fileName, dirName string) []string {
			return []string{fileName + "Tests"}
		},
	},
	{
		Name:        "DirToNamespace",
		Description: "Cache/Foo.cs -> .Cache.FooTests (directory as namespace)",
		Apply: func(fileName, dirName string) []string {
			if dirName != "" && dirName != "Source" && dirName != "src" {
				return []string{"." + dirName + "." + fileName}
			}
			return nil
		},
	},
}

// OptInHeuristics are additional heuristics that must be explicitly enabled
var OptInHeuristics = []TestHeuristic{
	{
		Name:        "ExtensionsToBase",
		Description: "FooExtensions.cs -> FooTests (extension methods with base class)",
		Apply: func(fileName, dirName string) []string {
			if strings.HasSuffix(fileName, "Extensions") {
				base := strings.TrimSuffix(fileName, "Extensions")
				return []string{base + "Tests"}
			}
			return nil
		},
	},
	{
		Name:        "InterfaceToImpl",
		Description: "IFoo.cs -> FooTests (interface to implementation tests)",
		Apply: func(fileName, dirName string) []string {
			if strings.HasPrefix(fileName, "I") && len(fileName) > 1 {
				// Check if second char is uppercase (IFoo, not "Internal")
				if len(fileName) > 1 && fileName[1] >= 'A' && fileName[1] <= 'Z' {
					impl := fileName[1:] // Strip leading "I"
					return []string{impl + "Tests"}
				}
			}
			return nil
		},
	},
	{
		Name:        "AlwaysCompositionRoot",
		Description: "Any .cs -> CompositionRootTests (DI container tests)",
		Apply: func(fileName, dirName string) []string {
			return []string{"CompositionRootTests"}
		},
	},
}

// DefaultHeuristics returns the names of heuristics enabled by default
func DefaultHeuristics() []string {
	return []string{"NameToNameTests", "DirToNamespace"}
}

// AllHeuristics returns all heuristics (default + opt-in)
func AllHeuristics() []TestHeuristic {
	all := make([]TestHeuristic, 0, len(AvailableHeuristics)+len(OptInHeuristics))
	all = append(all, AvailableHeuristics...)
	all = append(all, OptInHeuristics...)
	return all
}

// ParseHeuristics parses a comma-separated list of heuristic names
// Returns the enabled heuristics.
//   - "default" = default heuristics only
//   - "none" = no heuristics
//   - "default,ExtensionsToBase" = defaults + specific opt-in
//   - "NameToNameTests,InterfaceToImpl" = only specified ones
func ParseHeuristics(spec string) []TestHeuristic {
	if spec == "" || spec == "default" {
		return AvailableHeuristics
	}
	if spec == "none" {
		return nil
	}

	// Build lookup of all heuristics
	allByName := make(map[string]TestHeuristic)
	for _, h := range AvailableHeuristics {
		allByName[h.Name] = h
	}
	for _, h := range OptInHeuristics {
		allByName[h.Name] = h
	}

	var result []TestHeuristic
	seen := make(map[string]bool)

	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if seen[name] {
			continue
		}
		seen[name] = true

		if name == "default" {
			// Add all default heuristics
			for _, h := range AvailableHeuristics {
				if !seen[h.Name] {
					seen[h.Name] = true
					result = append(result, h)
				}
			}
		} else if h, ok := allByName[name]; ok {
			result = append(result, h)
		}
	}
	return result
}

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
