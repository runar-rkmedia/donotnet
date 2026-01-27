package main

import (
	"strings"
)

// Suggestion represents a performance or best-practice suggestion
type Suggestion struct {
	ID          string   // unique identifier for tracking shown state
	Title       string   // short title
	Description string   // explanation of why this helps
	Projects    []string // affected project names
	Link        string   // optional URL for more information
}

// SuggestionChecker is a function that checks projects and returns a suggestion if applicable
type SuggestionChecker func(projects []*Project) *Suggestion

// suggestionCheckers contains all registered suggestion checkers
var suggestionCheckers = []SuggestionChecker{
	checkParallelTestFramework,
}

// shownSuggestions tracks which suggestions have been shown this session
var shownSuggestions = make(map[string]bool)

// checkParallelTestFramework checks for test projects missing Meziantou.Xunit.ParallelTestFramework
func checkParallelTestFramework(projects []*Project) *Suggestion {
	var affectedProjects []string

	for _, p := range projects {
		if !p.IsTest {
			continue
		}

		// Check if the project uses xUnit (has a reference to xunit or xunit.core)
		hasXunit := false
		hasParallelFramework := false

		for _, pkg := range p.PackageReferences {
			pkgLower := strings.ToLower(pkg)
			if pkgLower == "xunit" || pkgLower == "xunit.core" {
				hasXunit = true
			}
			if pkgLower == "meziantou.xunit.paralleltestframework" {
				hasParallelFramework = true
			}
		}

		// Only suggest if the project uses xUnit but doesn't have the parallel framework
		if hasXunit && !hasParallelFramework {
			affectedProjects = append(affectedProjects, p.Name)
		}
	}

	if len(affectedProjects) == 0 {
		return nil
	}

	return &Suggestion{
		ID:    "parallel-test-framework",
		Title: "Enable parallel test execution within projects",
		Description: `xUnit runs test classes in parallel but not individual tests within a class.
   Adding Meziantou.Xunit.ParallelTestFramework allows tests within each class
   to run in parallel, potentially speeding up large test classes.`,
		Projects: affectedProjects,
		Link:     "https://github.com/meziantou/Meziantou.Xunit.ParallelTestFramework",
	}
}

// RunSuggestions runs all suggestion checkers and returns new suggestions
// that haven't been shown this session
func RunSuggestions(projects []*Project) []Suggestion {
	var suggestions []Suggestion

	for _, checker := range suggestionCheckers {
		suggestion := checker(projects)
		if suggestion == nil {
			continue
		}

		// Skip if already shown this session
		if shownSuggestions[suggestion.ID] {
			continue
		}

		// Mark as shown
		shownSuggestions[suggestion.ID] = true
		suggestions = append(suggestions, *suggestion)
	}

	return suggestions
}

// PrintSuggestions outputs suggestions using the terminal
func PrintSuggestions(suggestions []Suggestion) {
	if len(suggestions) == 0 {
		return
	}

	for _, s := range suggestions {
		// Use dim styling to not distract from main output
		if term.IsPlain() {
			term.Printf("\nTIP: %s\n", s.Title)
			term.Printf("   %s\n", s.Description)
			if len(s.Projects) > 0 {
				term.Printf("\n   Affected: %s\n", strings.Join(s.Projects, ", "))
			}
			if s.Link != "" {
				term.Printf("\n   See: %s\n", s.Link)
			}
		} else {
			term.Printf("\n%s💡 TIP:%s %s\n", colorYellow, colorReset, s.Title)
			term.Printf("%s   %s%s\n", colorDim, s.Description, colorReset)
			if len(s.Projects) > 0 {
				term.Printf("\n%s   Affected:%s %s\n", colorDim, colorReset, strings.Join(s.Projects, ", "))
			}
			if s.Link != "" {
				term.Printf("\n%s   See:%s %s\n", colorDim, colorReset, s.Link)
			}
		}
	}
	term.Printf("\n")
}
