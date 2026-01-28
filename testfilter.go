package main

import (
	"fmt"
	"io/fs"
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
	// ExcludedByUserFilter is true if all matched tests would be excluded by user's --filter
	// When true, the caller should skip running tests entirely
	ExcludedByUserFilter bool
	// ExcludedTraits lists the traits that caused exclusion (for verbose output)
	ExcludedTraits []string
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
				projectDir := findProjectDir(fullPath)
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
				projectDir := findProjectDir(fullPath)
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

// Regex to find test attributes in C# code
var testAttributeRegex = regexp.MustCompile(`\[(Test|Fact|Theory|TestMethod|TestCase)\b`)
var classRegex = regexp.MustCompile(`\bclass\s+(\w+)`)

// Regex patterns for extracting category/trait attributes from C# test files
// Matches: [Category("Live")], [Trait("Category", "Live")], [TestCategory("Live")]
var (
	// NUnit style: [Category("Live")]
	categoryAttrRegex = regexp.MustCompile(`\[Category\s*\(\s*"([^"]+)"\s*\)\]`)
	// xUnit style: [Trait("Category", "Live")]
	traitAttrRegex = regexp.MustCompile(`\[Trait\s*\(\s*"Category"\s*,\s*"([^"]+)"\s*\)\]`)
	// MSTest style: [TestCategory("Live")]
	testCategoryAttrRegex = regexp.MustCompile(`\[TestCategory\s*\(\s*"([^"]+)"\s*\)\]`)
	// Filter exclusion pattern: Category!=Live or Category != Live
	filterExclusionRegex = regexp.MustCompile(`Category\s*!=\s*(\w+)`)
	// Class definition with optional attributes above it
	// Captures: attributes block (group 1), class name (group 2)
	classBlockRegex = regexp.MustCompile(`(?ms)((?:\[[^\]]+\]\s*)*)\s*(?:public\s+|internal\s+|private\s+|protected\s+)*(?:abstract\s+|sealed\s+|static\s+)*class\s+(\w+)`)
	// Test method with optional attributes above it
	// Captures: attributes block (group 1), method name (group 2)
	testMethodBlockRegex = regexp.MustCompile(`(?ms)((?:\[[^\]]+\]\s*)+)\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:async\s+)?(?:Task|void|\w+)\s+(\w+)\s*\(`)
)

// ExtractCategoryTraits extracts all category traits from a C# test file
// Returns a slice of category names found (e.g., ["Live", "Slow"])
func ExtractCategoryTraits(filePath string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	return ExtractCategoryTraitsFromContent(string(content))
}

// ExtractCategoryTraitsFromContent extracts category traits from file content
func ExtractCategoryTraitsFromContent(content string) []string {
	// Strip comments before processing to avoid matching commented-out attributes
	content = stripCSharpComments(content)
	traitsMap := make(map[string]bool)

	// Find all Category attributes (NUnit)
	for _, match := range categoryAttrRegex.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	// Find all Trait("Category", "...") attributes (xUnit)
	for _, match := range traitAttrRegex.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	// Find all TestCategory attributes (MSTest)
	for _, match := range testCategoryAttrRegex.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	var traits []string
	for trait := range traitsMap {
		traits = append(traits, trait)
	}
	return traits
}

// stripCSharpComments removes C# single-line comments from content
// This is a simple implementation that handles the common case of // comments
func stripCSharpComments(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		// Find // that's not inside a string literal
		// Simple heuristic: just look for // and truncate
		// This may incorrectly truncate strings containing //, but that's rare in attributes
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// ParseFilterExclusions extracts excluded categories from a dotnet test filter
// e.g., "Category!=Live" returns ["Live"]
// e.g., "Category!=Live&Category!=Slow" returns ["Live", "Slow"]
func ParseFilterExclusions(filter string) []string {
	var exclusions []string
	for _, match := range filterExclusionRegex.FindAllStringSubmatch(filter, -1) {
		if len(match) >= 2 {
			exclusions = append(exclusions, match[1])
		}
	}
	return exclusions
}

// AreAllTraitsExcluded checks if all traits in the list are excluded by the filter
// Returns true if the traits list is non-empty and ALL of them are in the exclusion list
func AreAllTraitsExcluded(traits []string, excludedCategories []string) bool {
	if len(traits) == 0 || len(excludedCategories) == 0 {
		return false
	}

	excludedMap := make(map[string]bool)
	for _, cat := range excludedCategories {
		excludedMap[strings.ToLower(cat)] = true
	}

	for _, trait := range traits {
		if !excludedMap[strings.ToLower(trait)] {
			// Found a trait that's NOT excluded
			return false
		}
	}

	// All traits are excluded
	return true
}

// TestMethodInfo represents a test method with its traits
type TestMethodInfo struct {
	Name   string
	Traits []string // combined class-level + method-level traits
}

// AreAllTestsExcludedInFile analyzes a C# test file and determines if ALL test methods
// would be excluded by the given category filter exclusions.
// This properly handles:
// - Class-level traits (apply to all methods in the class)
// - Method-level traits (apply to specific test methods)
// - Multiple classes in the same file
// Returns (allExcluded bool, excludedTraits []string, testCount int)
func AreAllTestsExcludedInFile(content string, excludedCategories []string) (bool, []string, int) {
	if len(excludedCategories) == 0 {
		return false, nil, 0
	}

	// Strip comments to avoid matching commented-out attributes
	content = stripCSharpComments(content)

	excludedMap := make(map[string]bool)
	for _, cat := range excludedCategories {
		excludedMap[strings.ToLower(cat)] = true
	}

	// Find all classes in the file
	classMatches := classBlockRegex.FindAllStringSubmatchIndex(content, -1)
	if len(classMatches) == 0 {
		return false, nil, 0
	}

	var allTestMethods []TestMethodInfo
	var allExcludedTraits []string

	for i, classMatch := range classMatches {
		// classMatch indices: [fullStart, fullEnd, attrsStart, attrsEnd, nameStart, nameEnd]
		if len(classMatch) < 6 {
			continue
		}

		// Extract class attributes and name
		classAttrs := ""
		if classMatch[2] >= 0 && classMatch[3] >= 0 {
			classAttrs = content[classMatch[2]:classMatch[3]]
		}

		// Extract class-level traits
		classTraits := extractTraitsFromAttributes(classAttrs)

		// Find the class body (from class definition to next class or end of file)
		classStart := classMatch[0]
		classEnd := len(content)
		if i+1 < len(classMatches) {
			classEnd = classMatches[i+1][0]
		}
		classBody := content[classStart:classEnd]

		// Find test methods in this class
		methodMatches := testMethodBlockRegex.FindAllStringSubmatch(classBody, -1)
		for _, methodMatch := range methodMatches {
			if len(methodMatch) < 3 {
				continue
			}

			methodAttrs := methodMatch[1]
			methodName := methodMatch[2]

			// Check if this is actually a test method (has test attribute)
			if !testAttributeRegex.MatchString(methodAttrs) {
				continue
			}

			// Extract method-level traits
			methodTraits := extractTraitsFromAttributes(methodAttrs)

			// Combine class-level and method-level traits
			combinedTraits := make(map[string]bool)
			for _, t := range classTraits {
				combinedTraits[t] = true
			}
			for _, t := range methodTraits {
				combinedTraits[t] = true
			}

			var traits []string
			for t := range combinedTraits {
				traits = append(traits, t)
			}

			allTestMethods = append(allTestMethods, TestMethodInfo{
				Name:   methodName,
				Traits: traits,
			})
		}
	}

	if len(allTestMethods) == 0 {
		// No test methods found - can't determine, don't exclude
		return false, nil, 0
	}

	// Check if ALL test methods have at least one excluded trait
	for _, method := range allTestMethods {
		if len(method.Traits) == 0 {
			// Method has no traits, won't be excluded
			return false, nil, len(allTestMethods)
		}

		hasExcludedTrait := false
		for _, trait := range method.Traits {
			if excludedMap[strings.ToLower(trait)] {
				hasExcludedTrait = true
				allExcludedTraits = append(allExcludedTraits, trait)
				break
			}
		}

		if !hasExcludedTrait {
			// This method won't be excluded
			return false, nil, len(allTestMethods)
		}
	}

	// All test methods have at least one excluded trait
	return true, uniqueStrings(allExcludedTraits), len(allTestMethods)
}

// extractTraitsFromAttributes extracts category traits from an attributes block
func extractTraitsFromAttributes(attrs string) []string {
	traitsMap := make(map[string]bool)

	for _, match := range categoryAttrRegex.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}
	for _, match := range traitAttrRegex.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}
	for _, match := range testCategoryAttrRegex.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	var traits []string
	for trait := range traitsMap {
		traits = append(traits, trait)
	}
	return traits
}

// TestHeuristic defines a named heuristic for guessing test names from source files
type TestHeuristic struct {
	Name        string // Short identifier (e.g., "NameToNameTests")
	Description string // Human-readable description
	// Apply returns test patterns to match for a given source file
	// fileName is without extension, dirName is the immediate parent directory
	Apply func(fileName, dirName string) []string
}

// AvailableHeuristics lists heuristics enabled by default
// Empty by default - all heuristics are opt-in
var AvailableHeuristics = []TestHeuristic{}

// OptInHeuristics are heuristics that must be explicitly enabled via --heuristics flag
// These guess test names from source file names - useful but can be wrong
var OptInHeuristics = []TestHeuristic{
	{
		Name:        "TestFileOnly",
		Description: "FooTests.cs -> FooTests (filters to changed test file if it has test methods and isn't referenced by other tests)",
		Apply: func(fileName, dirName string) []string {
			// Only return the file if it's already a test file
			// For non-test files, return nil - meaning we can't determine what tests to run
			// Note: The actual safety check happens in getFilterWithHeuristics
			if strings.HasSuffix(fileName, "Tests") || strings.HasSuffix(fileName, "Test") {
				return []string{fileName}
			}
			return nil
		},
	},
	{
		Name:        "NameToNameTests",
		Description: "Foo.cs -> FooTests (direct name match, can miss or run wrong tests)",
		Apply: func(fileName, dirName string) []string {
			return []string{fileName + "Tests"}
		},
	},
	{
		Name:        "DirToNamespace",
		Description: "Cache/Foo.cs -> .Cache.Foo (matches tests with directory as namespace)",
		Apply: func(fileName, dirName string) []string {
			if dirName != "" && dirName != "Source" && dirName != "src" {
				return []string{"." + dirName + "." + fileName}
			}
			return nil
		},
	},
	{
		Name:        "ExtensionsToBase",
		Description: "FooExtensions.cs -> FooTests (assumes extension methods are tested with base class)",
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
	return []string{}
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
//   - "default,-DirToNamespace" = defaults minus specific one
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

	// First pass: collect additions and removals
	var additions []string
	disabled := make(map[string]bool)

	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if strings.HasPrefix(name, "-") {
			// Disable this heuristic
			disabled[name[1:]] = true
		} else {
			additions = append(additions, name)
		}
	}

	// Second pass: build result
	var result []TestHeuristic
	seen := make(map[string]bool)

	for _, name := range additions {
		if seen[name] || disabled[name] {
			continue
		}
		seen[name] = true

		if name == "default" {
			// Add all default heuristics (unless disabled)
			for _, h := range AvailableHeuristics {
				if !seen[h.Name] && !disabled[h.Name] {
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

// TestFileSafetyResult contains the analysis of whether a test file is safe to filter on
type TestFileSafetyResult struct {
	IsSafe          bool
	Reason          string
	ClassName       string
	HasTestMethods  bool
	IsReferencedBy  []string // other files that reference this file's types
}

// helperPatterns matches common test helper/fixture naming patterns
var helperPatterns = regexp.MustCompile(`(?i)(Helper|Fixture|Base|Utilities|Common|Shared|Mock|Fake|Stub|TestData|Setup)`)

// testMethodRegex matches test method declarations (attribute followed by method)
var testMethodRegex = regexp.MustCompile(`\[(Test|Fact|Theory|TestMethod|TestCase)[^\]]*\]\s*\n\s*(public|private|protected|internal)?\s*(async\s+)?(Task|void|\w+)\s+\w+\s*\(`)

// classDefinitionRegex extracts all class names and their base classes
var classDefinitionRegex = regexp.MustCompile(`\bclass\s+(\w+)(?:\s*:\s*([^{]+))?`)

// IsSafeTestFile analyzes a test file to determine if it's safe to filter on
// A file is NOT safe if:
// - It has no actual test methods (might be a base class or helper)
// - Its name suggests it's a helper/fixture/base class
// - Other test files in the same directory inherit from or reference its types
func IsSafeTestFile(filePath string, projectDir string) TestFileSafetyResult {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return TestFileSafetyResult{IsSafe: false, Reason: "cannot read file"}
	}

	contentStr := string(content)
	fileName := filepath.Base(filePath)
	nameWithoutExt := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	// Check 1: Does the file name suggest it's a helper/base class?
	if helperPatterns.MatchString(nameWithoutExt) {
		return TestFileSafetyResult{
			IsSafe: false,
			Reason: fmt.Sprintf("file name suggests helper/fixture: %s", nameWithoutExt),
		}
	}

	// Check 2: Does the file contain actual test methods?
	hasTestMethods := testMethodRegex.MatchString(contentStr)
	if !hasTestMethods {
		return TestFileSafetyResult{
			IsSafe:         false,
			HasTestMethods: false,
			Reason:         "no test methods found (may be a base class or helper)",
		}
	}

	// Check 3: Extract class names from this file
	classMatches := classDefinitionRegex.FindAllStringSubmatch(contentStr, -1)
	if len(classMatches) == 0 {
		return TestFileSafetyResult{
			IsSafe:         false,
			HasTestMethods: true,
			Reason:         "no class definition found",
		}
	}

	// Collect class names defined in this file
	var classNames []string
	for _, match := range classMatches {
		if len(match) >= 2 {
			classNames = append(classNames, match[1])
		}
	}

	// Check 4: See if other test files in the project reference these classes
	referencingFiles := findReferencingFiles(projectDir, filePath, classNames)
	if len(referencingFiles) > 0 {
		return TestFileSafetyResult{
			IsSafe:         false,
			HasTestMethods: true,
			ClassName:      classNames[0],
			IsReferencedBy: referencingFiles,
			Reason:         fmt.Sprintf("referenced by %d other test file(s)", len(referencingFiles)),
		}
	}

	return TestFileSafetyResult{
		IsSafe:         true,
		HasTestMethods: true,
		ClassName:      classNames[0],
		Reason:         "file contains test methods and is not referenced by other test files",
	}
}

// findReferencingFiles scans test files in projectDir for references to the given class names
func findReferencingFiles(projectDir string, excludeFile string, classNames []string) []string {
	if projectDir == "" || len(classNames) == 0 {
		return nil
	}

	var referencingFiles []string

	// Build a regex to find references to any of the class names
	// Look for: inheritance (: ClassName), instantiation (new ClassName), type usage (ClassName.)
	patterns := make([]string, 0, len(classNames))
	for _, name := range classNames {
		// Match: inheritance, generic parameter, new, method param, property type, variable decl
		patterns = append(patterns, fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(name)))
	}
	referenceRegex := regexp.MustCompile(strings.Join(patterns, "|"))

	// Walk the project directory looking for test files
	filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Skip the file we're checking
		if path == excludeFile {
			return nil
		}

		// Skip non-.cs files and non-test files
		if d.IsDir() {
			name := d.Name()
			if name == "bin" || name == "obj" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".cs") {
			return nil
		}

		// Only check test files (by name convention)
		fileName := d.Name()
		nameWithoutExt := strings.TrimSuffix(fileName, ".cs")
		if !strings.HasSuffix(nameWithoutExt, "Tests") && !strings.HasSuffix(nameWithoutExt, "Test") {
			return nil
		}

		// Read and check for references
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		if referenceRegex.Match(content) {
			relPath, _ := filepath.Rel(projectDir, path)
			if relPath == "" {
				relPath = filepath.Base(path)
			}
			referencingFiles = append(referencingFiles, relPath)
		}

		return nil
	})

	return referencingFiles
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

// findProjectDir finds the project directory by looking for .csproj files
// in parent directories of the given file path
func findProjectDir(filePath string) string {
	dir := filepath.Dir(filePath)
	for dir != "" && dir != "/" && dir != "." {
		// Check if this directory contains a .csproj file
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".csproj") {
					return dir
				}
			}
		}
		dir = filepath.Dir(dir)
	}
	// Fallback: use the file's immediate directory
	return filepath.Dir(filePath)
}

// containsString checks if a string slice contains a specific string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
