// Package testfilter provides test filtering capabilities for donotnet.
// It analyzes changed files and determines which tests need to run,
// using coverage data, heuristics, and naming conventions.
package testfilter

import (
	"fmt"
	"os"
	"path/filepath"
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
	// ExcludedByUserFilter is true if all matched tests would be excluded by user's --filter
	// When true, the caller should skip running tests entirely
	ExcludedByUserFilter bool
	// ExcludedTraits lists the traits that caused exclusion (for verbose output)
	ExcludedTraits []string
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

// SetCoverageMaps sets the per-test coverage maps for coverage-based filtering
func (tf *TestFilter) SetCoverageMaps(maps map[string]*TestCoverageMap) {
	tf.CoverageMaps = maps
}

// GetFilter analyzes changed files for a project and returns a filter if possible
// userFilter is the user's --filter argument (e.g., "Category!=Live") used to detect
// when all matched tests would be excluded
func (tf *TestFilter) GetFilter(projectPath string, gitRoot string, userFilter string) FilterResult {
	// Parse user filter for category exclusions
	excludedCategories := ParseFilterExclusions(userFilter)

	// First, try coverage-based filtering using all changed files
	// This works even when the changed file is in a different project than the test project
	if len(tf.AllChangedFiles) > 0 && len(tf.CoverageMaps) > 0 {
		result := tf.getFilterWithCoverage(projectPath, tf.AllChangedFiles, nil)
		if result.CanFilter {
			// Check if all tests would be excluded by user filter
			result = tf.checkUserFilterExclusion(result, gitRoot, excludedCategories)
			return result
		}
		// If coverage lookup found no tests or file not covered, try heuristics
	}

	// Try heuristic-based filtering using all changed files
	if len(tf.AllChangedFiles) > 0 {
		result := tf.getFilterWithHeuristics(tf.AllChangedFiles, gitRoot)
		if result.CanFilter {
			// Check if all tests would be excluded by user filter
			result = tf.checkUserFilterExclusion(result, gitRoot, excludedCategories)
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

	result := FilterResult{
		CanFilter:   true,
		TestFilter:  filter,
		Reason:      fmt.Sprintf("only test files changed: %s", strings.Join(testClasses, ", ")),
		TestClasses: testClasses,
	}

	// Check if all tests would be excluded by user filter
	result = tf.checkUserFilterExclusion(result, gitRoot, excludedCategories)
	return result
}

// checkUserFilterExclusion checks if all test classes in the result would be excluded
// by the user's category filter (e.g., Category!=Live)
func (tf *TestFilter) checkUserFilterExclusion(result FilterResult, gitRoot string, excludedCategories []string) FilterResult {
	if len(excludedCategories) == 0 || !result.CanFilter {
		return result
	}

	// Check each changed file that corresponds to a test class
	var allExcludedTraits []string
	allExcluded := true
	matchedFilesCount := 0

	for _, file := range tf.AllChangedFiles {
		// Only check .cs files that look like test files
		if !strings.HasSuffix(strings.ToLower(file), ".cs") {
			continue
		}

		fileName := filepath.Base(file)
		nameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))

		// Check if this file corresponds to one of our test classes
		isMatchedTestFile := false
		for _, class := range result.TestClasses {
			if class == nameWithoutExt {
				isMatchedTestFile = true
				break
			}
		}

		if !isMatchedTestFile {
			continue
		}

		matchedFilesCount++

		// Read file content and analyze if ALL test methods would be excluded
		fullPath := filepath.Join(gitRoot, file)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			// Can't read file, be conservative and don't exclude
			allExcluded = false
			break
		}

		excluded, traits, testCount := AreAllTestsExcludedInFile(string(content), excludedCategories)
		if testCount == 0 {
			// No test methods found - be conservative, don't skip
			allExcluded = false
			break
		}

		if excluded {
			allExcludedTraits = append(allExcludedTraits, traits...)
		} else {
			// Not all tests in this file are excluded
			allExcluded = false
			break
		}
	}

	if allExcluded && matchedFilesCount > 0 && len(allExcludedTraits) > 0 {
		// All matched test files have only excluded categories
		result.ExcludedByUserFilter = true
		result.ExcludedTraits = uniqueStrings(allExcludedTraits)
		result.Reason = fmt.Sprintf("all tests excluded by user filter (traits: %s)", strings.Join(result.ExcludedTraits, ", "))
	}

	return result
}

// getFilterWithHeuristics uses naming conventions to guess which tests to run
func (tf *TestFilter) getFilterWithHeuristics(changedFiles []string, gitRoot string) FilterResult {
	// If no heuristics are enabled, return early
	if len(tf.Heuristics) == 0 {
		return FilterResult{
			CanFilter: false,
			Reason:    "heuristics disabled",
		}
	}

	// Check if TestFileOnly heuristic is enabled (for safe test file detection)
	testFileOnlyEnabled := false
	for _, h := range tf.Heuristics {
		if h.Name == "TestFileOnly" {
			testFileOnlyEnabled = true
			break
		}
	}

	testsToRun := make(map[string]bool)
	var nonCsFiles []string
	var usedHeuristics []string
	var unsafeTestFiles []string // test files that failed safety check

	for _, file := range changedFiles {
		fileName := filepath.Base(file)
		ext := strings.ToLower(filepath.Ext(fileName))

		// Skip non-.cs files - they could be .csproj, .razor, etc.
		if ext != ".cs" {
			nonCsFiles = append(nonCsFiles, fileName)
			continue
		}

		nameWithoutExt := strings.TrimSuffix(fileName, ext)
		fullPath := filepath.Join(gitRoot, file)

		// If this is already a test file, check if it's safe to filter on
		if strings.HasSuffix(nameWithoutExt, "Tests") || strings.HasSuffix(nameWithoutExt, "Test") {
			if testFileOnlyEnabled {
				// Find project directory (look for .csproj in parent dirs)
				projectDir := FindProjectDir(fullPath)
				safetyResult := IsSafeTestFile(fullPath, projectDir)

				if safetyResult.IsSafe {
					testsToRun[nameWithoutExt] = true
					if !containsString(usedHeuristics, "TestFileOnly") {
						usedHeuristics = append(usedHeuristics, "TestFileOnly")
					}
				} else {
					// Track why this test file wasn't safe
					unsafeTestFiles = append(unsafeTestFiles, fmt.Sprintf("%s (%s)", nameWithoutExt, safetyResult.Reason))
				}
			} else {
				// No safety check, just add it (legacy behavior)
				testsToRun[nameWithoutExt] = true
			}
			continue
		}

		// Check if this file contains test attributes (might be a test file with non-standard name)
		if isTestOnlyFile(fullPath) {
			if testFileOnlyEnabled {
				projectDir := FindProjectDir(fullPath)
				safetyResult := IsSafeTestFile(fullPath, projectDir)
				if safetyResult.IsSafe {
					testsToRun[nameWithoutExt] = true
					if !containsString(usedHeuristics, "TestFileOnly") {
						usedHeuristics = append(usedHeuristics, "TestFileOnly")
					}
				} else {
					unsafeTestFiles = append(unsafeTestFiles, fmt.Sprintf("%s (%s)", nameWithoutExt, safetyResult.Reason))
				}
			} else {
				testsToRun[nameWithoutExt] = true
			}
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
		reason := "no heuristic matches found"
		if len(unsafeTestFiles) > 0 {
			reason = fmt.Sprintf("test file(s) not safe to filter: %s", strings.Join(unsafeTestFiles, "; "))
		}
		return FilterResult{
			CanFilter: false,
			Reason:    reason,
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

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
