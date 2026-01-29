package testfilter

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TestFileSafetyResult contains the analysis of whether a test file is safe to filter on
type TestFileSafetyResult struct {
	IsSafe         bool
	Reason         string
	ClassName      string
	HasTestMethods bool
	IsReferencedBy []string // other files that reference this file's types
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

// isTestOnlyFile checks if a file contains only test classes (has test attributes)
// This is a heuristic - if the file has test attributes, we assume it's a test file
func isTestOnlyFile(filePath string) bool {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	// If the file has test attributes, consider it a test file
	return TestAttributeRegex.Match(content)
}

// ExtractTestClassName tries to extract the test class name from file content
// Returns empty string if no class found
func ExtractTestClassName(filePath string) string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	matches := ClassRegex.FindStringSubmatch(string(content))
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// FindProjectDir finds the project directory by looking for .csproj files
// in parent directories of the given file path
func FindProjectDir(filePath string) string {
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
