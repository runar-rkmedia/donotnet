package testfilter

import "strings"

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
