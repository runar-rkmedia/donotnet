package testfilter

import (
	"os"
	"regexp"
	"strings"
)

// TestAttributeRegex matches test attributes in C# code
var TestAttributeRegex = regexp.MustCompile(`\[(Test|Fact|Theory|TestMethod|TestCase)\b`)
// ClassRegex matches class definitions in C# code
var ClassRegex = regexp.MustCompile(`\bclass\s+(\w+)`)

// Regex patterns for extracting category/trait attributes from C# test files
// Matches: [Category("Live")], [Trait("Category", "Live")], [TestCategory("Live")]
var (
	// CategoryAttrRegex matches NUnit style: [Category("Live")]
	CategoryAttrRegex = regexp.MustCompile(`\[Category\s*\(\s*"([^"]+)"\s*\)\]`)
	// TraitAttrRegex matches xUnit style: [Trait("Category", "Live")]
	TraitAttrRegex = regexp.MustCompile(`\[Trait\s*\(\s*"Category"\s*,\s*"([^"]+)"\s*\)\]`)
	// TestCategoryAttrRegex matches MSTest style: [TestCategory("Live")]
	TestCategoryAttrRegex = regexp.MustCompile(`\[TestCategory\s*\(\s*"([^"]+)"\s*\)\]`)
	// filterExclusionRegex matches filter exclusion pattern: Category!=Live or Category != Live
	filterExclusionRegex = regexp.MustCompile(`Category\s*!=\s*(\w+)`)
	// classBlockRegex matches class definition with optional attributes above it
	// Captures: attributes block (group 1), class name (group 2)
	classBlockRegex = regexp.MustCompile(`(?ms)((?:\[[^\]]+\]\s*)*)\s*(?:public\s+|internal\s+|private\s+|protected\s+)*(?:abstract\s+|sealed\s+|static\s+)*class\s+(\w+)`)
	// testMethodBlockRegex matches test method with optional attributes above it
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
	content = StripCSharpComments(content)
	traitsMap := make(map[string]bool)

	// Find all Category attributes (NUnit)
	for _, match := range CategoryAttrRegex.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	// Find all Trait("Category", "...") attributes (xUnit)
	for _, match := range TraitAttrRegex.FindAllStringSubmatch(content, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}

	// Find all TestCategory attributes (MSTest)
	for _, match := range TestCategoryAttrRegex.FindAllStringSubmatch(content, -1) {
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

// StripCSharpComments removes C# single-line comments from content
// This is a simple implementation that handles the common case of // comments
func StripCSharpComments(content string) string {
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
	content = StripCSharpComments(content)

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
		classTraits := ExtractTraitsFromAttributes(classAttrs)

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
			if !TestAttributeRegex.MatchString(methodAttrs) {
				continue
			}

			// Extract method-level traits
			methodTraits := ExtractTraitsFromAttributes(methodAttrs)

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

// ExtractTraitsFromAttributes extracts category traits from an attributes block
func ExtractTraitsFromAttributes(attrs string) []string {
	traitsMap := make(map[string]bool)

	for _, match := range CategoryAttrRegex.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}
	for _, match := range TraitAttrRegex.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 2 {
			traitsMap[match[1]] = true
		}
	}
	for _, match := range TestCategoryAttrRegex.FindAllStringSubmatch(attrs, -1) {
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
