package testfilter

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// TraitMap holds class-level and method-level trait mappings for a project.
type TraitMap struct {
	// ClassTraits maps fully qualified class name -> list of traits
	ClassTraits map[string][]string
	// MethodTraits maps fully qualified method name -> list of traits
	MethodTraits map[string][]string
}

// BuildTraitMap walks a project directory and builds a map of traits per class and method.
func BuildTraitMap(projectDir string) TraitMap {
	tm := TraitMap{
		ClassTraits:  make(map[string][]string),
		MethodTraits: make(map[string][]string),
	}

	namespaceRegex := regexp.MustCompile(`(?m)^\s*namespace\s+([\w.]+)`)

	filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".cs") {
			return nil
		}
		if strings.Contains(path, string(os.PathSeparator)+"obj"+string(os.PathSeparator)) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := StripCSharpComments(string(content))

		var namespace string
		if m := namespaceRegex.FindStringSubmatch(src); m != nil {
			namespace = m[1]
		}

		// Find classes and their traits
		classMatches := classBlockRegex.FindAllStringSubmatchIndex(src, -1)
		for i, classMatch := range classMatches {
			if len(classMatch) < 6 {
				continue
			}

			classAttrs := ""
			if classMatch[2] >= 0 && classMatch[3] >= 0 {
				classAttrs = src[classMatch[2]:classMatch[3]]
			}
			className := src[classMatch[4]:classMatch[5]]

			fqClassName := className
			if namespace != "" {
				fqClassName = namespace + "." + className
			}

			classTraits := ExtractTraitsFromAttributes(classAttrs)
			if len(classTraits) > 0 {
				tm.ClassTraits[fqClassName] = classTraits
			}

			// Find methods within this class body
			classStart := classMatch[0]
			classEnd := len(src)
			if i+1 < len(classMatches) {
				classEnd = classMatches[i+1][0]
			}
			classBody := src[classStart:classEnd]

			methodMatches := testMethodBlockRegex.FindAllStringSubmatch(classBody, -1)
			for _, methodMatch := range methodMatches {
				if len(methodMatch) < 3 {
					continue
				}
				methodAttrs := methodMatch[1]
				methodName := methodMatch[2]
				if !TestAttributeRegex.MatchString(methodAttrs) {
					continue
				}

				methodTraits := ExtractTraitsFromAttributes(methodAttrs)
				if len(methodTraits) > 0 {
					fqMethodName := fqClassName + "." + methodName
					tm.MethodTraits[fqMethodName] = methodTraits
				}
			}
		}

		return nil
	})

	return tm
}

// GetTraitsForTest returns combined class-level and method-level traits for a test name.
func (tm TraitMap) GetTraitsForTest(testName string) []string {
	// Strip parameters
	baseName := testName
	if idx := strings.Index(testName, "("); idx > 0 {
		baseName = testName[:idx]
	}

	traits := make(map[string]bool)

	// Add class-level traits
	className := baseName
	if idx := strings.LastIndex(baseName, "."); idx > 0 {
		className = baseName[:idx]
	}
	for _, t := range tm.ClassTraits[className] {
		traits[t] = true
	}

	// Add method-level traits
	for _, t := range tm.MethodTraits[baseName] {
		traits[t] = true
	}

	if len(traits) == 0 {
		return nil
	}

	result := make([]string, 0, len(traits))
	for t := range traits {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// AllTraits returns a deduplicated sorted list of all traits in the map.
func (tm TraitMap) AllTraits() []string {
	traits := make(map[string]bool)
	for _, ts := range tm.ClassTraits {
		for _, t := range ts {
			traits[t] = true
		}
	}
	for _, ts := range tm.MethodTraits {
		for _, t := range ts {
			traits[t] = true
		}
	}
	result := make([]string, 0, len(traits))
	for t := range traits {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
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
