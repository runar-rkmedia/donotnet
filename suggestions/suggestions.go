package suggestions

import (
	"strings"

	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// Suggestion represents a performance or best-practice suggestion.
type Suggestion struct {
	ID          string   // unique identifier for tracking shown state
	Title       string   // short title
	Description string   // explanation of why this helps
	Projects    []string // affected project names
	Link        string   // optional URL for more information
}

// Checker is a function that checks projects and returns a suggestion if applicable.
type Checker func(projects []*project.Project) *Suggestion

// checkers contains all registered suggestion checkers.
var checkers = []Checker{
	checkParallelTestFramework,
}

// shownSuggestions tracks which suggestions have been shown this session.
var shownSuggestions = make(map[string]bool)

// checkParallelTestFramework checks for test projects missing Meziantou.Xunit.ParallelTestFramework.
func checkParallelTestFramework(projects []*project.Project) *Suggestion {
	var affectedProjects []string

	for _, p := range projects {
		if !p.IsTest {
			continue
		}

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

// Run runs all suggestion checkers and returns new suggestions
// that haven't been shown this session.
func Run(projects []*project.Project) []Suggestion {
	var result []Suggestion
	for _, checker := range checkers {
		s := checker(projects)
		if s == nil || shownSuggestions[s.ID] {
			continue
		}
		shownSuggestions[s.ID] = true
		result = append(result, *s)
	}
	return result
}

// CheckCoverage checks coverage staleness and returns a suggestion if coverage
// is missing or stale. Returns nil if coverage is fresh or not applicable.
func CheckCoverage(gitRoot string, stalenessCheck string) *Suggestion {
	method := coverage.ParseStalenessMethod(stalenessCheck)
	id, title, desc := coverage.GetSuggestion(gitRoot, method)
	if id == "" {
		return nil
	}
	return &Suggestion{
		ID:          id,
		Title:       title,
		Description: desc,
	}
}

// PrintOnce prints a suggestion if it hasn't been shown this session.
func PrintOnce(s *Suggestion) {
	if s == nil || shownSuggestions[s.ID] {
		return
	}
	shownSuggestions[s.ID] = true
	Print([]Suggestion{*s})
}

// Print outputs suggestions using the terminal.
func Print(suggestions []Suggestion) {
	if len(suggestions) == 0 {
		return
	}
	for _, s := range suggestions {
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
			term.Printf("\n%s💡 TIP:%s %s\n", term.ColorYellow, term.ColorReset, s.Title)
			term.Printf("%s   %s%s\n", term.ColorDim, s.Description, term.ColorReset)
			if len(s.Projects) > 0 {
				term.Printf("\n%s   Affected:%s %s\n", term.ColorDim, term.ColorReset, strings.Join(s.Projects, ", "))
			}
			if s.Link != "" {
				term.Printf("\n%s   See:%s %s\n", term.ColorDim, term.ColorReset, s.Link)
			}
		}
	}
	term.Printf("\n")
}
