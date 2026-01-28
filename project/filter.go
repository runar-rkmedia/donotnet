package project

import (
	"path/filepath"
	"strings"
)

// SkipDirs are directories that should be skipped when walking .NET projects
var SkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"bin":          true,
	"obj":          true,
	".vs":          true,
	"TestResults":  true,
}

// RelevantSourceExtensions are file extensions that are relevant for .NET source code
var RelevantSourceExtensions = map[string]bool{
	".cs":      true,
	".csproj":  true,
	".razor":   true,
	".props":   true,
	".targets": true,
}

// ShouldSkipDir returns true if the directory should be skipped during walks
func ShouldSkipDir(name string) bool {
	return SkipDirs[name]
}

// IsRelevantSourceExt returns true if the file extension is relevant for .NET source code
func IsRelevantSourceExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return RelevantSourceExtensions[ext]
}

// IsRelevantForCoverage returns true if a file path is relevant for coverage staleness checks.
// Excludes documentation, CI/CD configs, and other non-code files.
func IsRelevantForCoverage(path string) bool {
	// Check extension first
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".cs", ".csproj", ".props", ".targets":
		// These are relevant, continue checking path
	default:
		return false
	}

	// Exclude common non-code paths
	lowerPath := strings.ToLower(filepath.ToSlash(path))

	excludePrefixes := []string{
		"docs/", "doc/", "documentation/",
		".github/", ".gitlab/", ".azure/",
		"cicd/", "ci/", "cd/",
		"scripts/",
	}
	for _, prefix := range excludePrefixes {
		if strings.HasPrefix(lowerPath, prefix) {
			return false
		}
	}

	// Exclude common non-code files by name
	baseName := strings.ToLower(filepath.Base(path))
	excludeNames := []string{
		"readme.md", "readme.txt", "readme",
		"changelog.md", "changelog.txt", "changelog",
		"license", "license.md", "license.txt",
		".gitignore", ".editorconfig", ".dockerignore",
	}
	for _, name := range excludeNames {
		if baseName == name {
			return false
		}
	}

	return true
}

// IsTestFile returns true if the file appears to be a test file based on naming conventions
func IsTestFile(path string) bool {
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	nameWithoutExt := strings.TrimSuffix(name, ext)

	return strings.HasSuffix(nameWithoutExt, "Tests") ||
		strings.HasSuffix(nameWithoutExt, "Test")
}

// IsTestProject returns true if the path appears to be a test project
func IsTestProject(path string) bool {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".csproj")

	return strings.HasSuffix(name, ".Tests") ||
		strings.HasSuffix(name, ".Test") ||
		strings.HasSuffix(name, "Tests")
}
