package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// watchAction represents an action triggered by a keypress in watch mode.
type watchAction int

const (
	actionNone watchAction = iota
	actionRerun
	actionRunAll
	actionRunFailed
	actionQuit
	actionFilterProject
	actionFilterTest
	actionFilterTrait
	actionHelp
)

// watchOverrides holds user-applied overrides that persist across watch runs.
type watchOverrides struct {
	projects   []string // if set, only run these project paths
	testFilter string   // if set, add as extra --filter
	traitExpr  string   // if set, add as trait filter expression (e.g. "Category=Live" or "Category!=Live")
}

// hasAny returns true if any override is active.
func (o *watchOverrides) hasAny() bool {
	return len(o.projects) > 0 || o.testFilter != "" || o.traitExpr != ""
}

// clear resets all overrides.
func (o *watchOverrides) clear() {
	o.projects = nil
	o.testFilter = ""
	o.traitExpr = ""
}

// statusText returns a human-readable summary of active overrides.
func (o *watchOverrides) statusText() string {
	var parts []string
	if len(o.projects) > 0 {
		names := make([]string, len(o.projects))
		for i, p := range o.projects {
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			names[i] = name
		}
		parts = append(parts, fmt.Sprintf("projects: %s", strings.Join(names, ", ")))
	}
	if o.testFilter != "" {
		parts = append(parts, fmt.Sprintf("filter: %s", o.testFilter))
	}
	if o.traitExpr != "" {
		parts = append(parts, fmt.Sprintf("trait: %s", o.traitExpr))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, ", ") + "]"
}

// watchTestListCache discovers and caches test names in the background.
type watchTestListCache struct {
	mu     sync.Mutex
	lists  map[string][]string // project name → test names
	done   chan struct{}       // closed when background discovery completes
}

// newWatchTestListCache starts background test discovery for all test projects.
// Cached results are returned immediately; cache misses trigger dotnet test --list-tests.
func newWatchTestListCache(ctx context.Context, r *Runner) *watchTestListCache {
	c := &watchTestListCache{
		lists: make(map[string][]string),
		done:  make(chan struct{}),
	}

	// Collect what's already cached synchronously (fast)
	tlc := newTestListCache(r.db, r.gitRoot, r.forwardGraph)
	var uncached []*project.Project
	for _, p := range r.projects {
		if !p.IsTest {
			continue
		}
		tests := tlc.LookupTestList(p)
		if len(tests) > 0 {
			c.lists[p.Name] = tests
		} else {
			uncached = append(uncached, p)
		}
	}

	// Discover uncached projects in background
	go func() {
		defer close(c.done)
		for _, p := range uncached {
			if ctx.Err() != nil {
				return
			}
			absPath := filepath.Join(r.gitRoot, p.Path)
			discovered, err := coverage.ListTests(ctx, r.gitRoot, absPath)
			if err != nil {
				term.Verbose("  failed to list tests for %s: %v", p.Name, err)
				continue
			}
			if len(discovered) > 0 {
				tlc.StoreTestList(p, discovered)
				c.mu.Lock()
				c.lists[p.Name] = discovered
				c.mu.Unlock()
			}
		}
	}()

	return c
}

// get returns the current test lists. If background discovery is still running,
// it waits briefly then returns whatever is available.
func (c *watchTestListCache) get() map[string][]string {
	// Wait up to 5 seconds for background discovery to finish
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		term.Dim("Test discovery still running, showing partial results...")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Return a copy so caller can't race with background writes
	result := make(map[string][]string, len(c.lists))
	for k, v := range c.lists {
		result[k] = v
	}
	return result
}

// mapKeyToAction maps a single byte to a watch action.
func mapKeyToAction(key byte) watchAction {
	switch key {
	case '\r', '\n':
		return actionRerun
	case 'a':
		return actionRunAll
	case 'f':
		return actionRunFailed
	case 'q', 3: // 3 = Ctrl-C (raw mode intercepts the signal)
		return actionQuit
	case 'p':
		return actionFilterProject
	case 't':
		return actionFilterTest
	case 'T':
		return actionFilterTrait
	case 'h', '?':
		return actionHelp
	default:
		return actionNone
	}
}

// printHelp prints the watch mode help menu.
func printHelp() {
	term.Println()
	term.Info("Watch mode commands:")
	term.Dim("  Enter  Force rerun")
	term.Dim("  a      Run all (ignore cache)")
	term.Dim("  f      Run failed only")
	term.Dim("  p      Filter by project")
	term.Dim("  t      Filter by test name")
	term.Dim("  T      Filter by trait")
	term.Dim("  h      Show this help")
	term.Dim("  q      Quit")
	term.Println()
}

// printWatchHint prints the compact hint shown after each run.
func printWatchHint(overrides *watchOverrides) {
	status := overrides.statusText()
	term.Dim("Press h for help, q to quit%s", status)
}

// handleFilterProject prompts the user to select from a numbered list of test projects.
// Returns the selected project paths, or nil if cancelled.
func handleFilterProject(kr *term.KeyReader, projects []*project.Project, current []string) []string {
	// Collect test projects
	var testProjects []*project.Project
	for _, p := range projects {
		if p.IsTest {
			testProjects = append(testProjects, p)
		}
	}
	if len(testProjects) == 0 {
		term.Warn("No test projects found")
		return current
	}

	sort.Slice(testProjects, func(i, j int) bool {
		return testProjects[i].Name < testProjects[j].Name
	})

	// Mark currently active projects
	activeSet := make(map[string]bool)
	for _, p := range current {
		activeSet[p] = true
	}

	term.Println()
	term.Info("Select test project(s) (comma-separated numbers, or empty to clear):")
	for i, p := range testProjects {
		marker := "  "
		if activeSet[p.Path] {
			marker = term.Color(term.ColorGreen) + "* " + term.Color(term.ColorReset)
		}
		term.Printf("  %s%d) %s\n", marker, i+1, p.Name)
	}

	input, ok := kr.ReadLine(term.Color(term.ColorCyan) + "> " + term.Color(term.ColorReset))
	if !ok || strings.TrimSpace(input) == "" {
		if len(current) > 0 {
			term.Dim("Project filter cleared")
			return nil
		}
		term.Dim("Cancelled")
		return current
	}

	var selected []string
	parts := strings.Split(input, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		num, err := strconv.Atoi(part)
		if err != nil || num < 1 || num > len(testProjects) {
			term.Warn("Invalid selection: %s", part)
			return current
		}
		selected = append(selected, testProjects[num-1].Path)
	}

	// Convert paths to names for display
	var names []string
	for _, path := range selected {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		names = append(names, name)
	}
	term.Info("Filtering to: %s", strings.Join(names, ", "))
	return selected
}

// testTraitInfo holds per-test trait data and active category filters
// for annotating the test list display.
type testTraitInfo struct {
	traitMaps map[string]testfilter.TraitMap // project name → trait map
	included  map[string]bool               // categories included by active filters
	excluded  map[string]bool               // categories excluded by active filters
}

// testEntry pairs a test name with its project.
type testEntry struct {
	name    string
	project string
}

// renderTestList prints tests matching filter, returns line count.
// If filter is empty, shows all (up to limit). Uses case-insensitive substring matching.
func renderTestList(tests []testEntry, filter string, traitInfo *testTraitInfo, limit int, singleProject bool) int {
	dim := term.Color(term.ColorDim)
	reset := term.Color(term.ColorReset)
	red := term.Color(term.ColorRed)

	var visible []testEntry
	if filter == "" {
		visible = tests
	} else {
		lower := strings.ToLower(filter)
		for _, t := range tests {
			if strings.Contains(strings.ToLower(t.name), lower) {
				visible = append(visible, t)
			}
		}
	}

	if len(visible) == 0 {
		if filter != "" {
			term.Dim("  (no matches)")
			return 1
		}
		return 0
	}

	cap := limit
	if cap <= 0 || cap > len(visible) {
		cap = len(visible)
	}

	lines := 0
	prevName := ""
	for i := 0; i < cap; i++ {
		t := visible[i]

		// Find common prefix with previous entry for dim rendering
		commonLen := 0
		if prevName != "" {
			cl := len(prevName)
			if len(t.name) < cl {
				cl = len(t.name)
			}
			for commonLen < cl && t.name[commonLen] == prevName[commonLen] {
				commonLen++
			}
			if idx := strings.LastIndex(t.name[:commonLen], "."); idx >= 0 {
				commonLen = idx + 1
			} else {
				commonLen = 0
			}
		}
		prevName = t.name

		// Format name with dim prefix
		var formatted strings.Builder
		inDim := false
		for j, ch := range t.name {
			wantDim := j < commonLen || ch == '.' || ch == '_'
			if wantDim != inDim {
				if wantDim {
					formatted.WriteString(dim)
				} else {
					formatted.WriteString(reset)
				}
				inDim = wantDim
			}
			formatted.WriteRune(ch)
		}
		if inDim {
			formatted.WriteString(reset)
		}

		// Build trait annotation
		var traitSuffix string
		if traitInfo != nil {
			if tm, ok := traitInfo.traitMaps[t.project]; ok {
				traits := tm.GetTraitsForTest(t.name)
				if len(traits) > 0 {
					traitSuffix = " " + dim + "[" + strings.Join(traits, ", ") + "]" + reset

					// Check if any trait is excluded by active filters
					excluded := false
					for _, tr := range traits {
						if traitInfo.excluded[tr] {
							excluded = true
							break
						}
					}
					// Check if there's an include filter and this test's traits don't match
					if !excluded && len(traitInfo.included) > 0 {
						hasIncluded := false
						for _, tr := range traits {
							if traitInfo.included[tr] {
								hasIncluded = true
								break
							}
						}
						if !hasIncluded {
							excluded = true
						}
					}
					if excluded {
						traitSuffix += " " + red + "excluded" + reset
					}
				}
			}
		}

		if singleProject {
			term.Printf("  %s%d)%s %s%s\n", dim, i+1, reset, formatted.String(), traitSuffix)
		} else {
			term.Printf("  %s%d)%s %s %s(%s)%s%s\n",
				dim, i+1, reset,
				formatted.String(),
				dim, t.project, reset,
				traitSuffix)
		}
		lines++
	}

	if cap < len(visible) {
		term.Dim("  ... and %d more", len(visible)-cap)
		lines++
	}

	return lines
}

// handleFilterTest shows discovered tests and lets the user pick or type a filter.
// testLists maps project name → list of fully qualified test names (from cache).
// traitInfo provides optional trait annotations (may be nil).
func handleFilterTest(kr *term.KeyReader, testLists map[string][]string, current string, traitInfo *testTraitInfo) string {
	term.Println()
	if current != "" {
		term.Dim("Current filter: %s", current)
	}

	// Flatten and deduplicate test names across projects
	var allTests []testEntry
	seen := make(map[string]bool)
	for proj, tests := range testLists {
		for _, t := range tests {
			if !seen[t] {
				seen[t] = true
				allTests = append(allTests, testEntry{name: t, project: proj})
			}
		}
	}
	sort.Slice(allTests, func(i, j int) bool {
		return allTests[i].name < allTests[j].name
	})

	singleProject := len(testLists) == 1

	if len(allTests) == 0 {
		term.Info("No tests discovered. Enter a test name filter (partial match), or empty to clear:")
		input, ok := kr.ReadLine(term.Color(term.ColorCyan) + "> " + term.Color(term.ColorReset))
		if !ok {
			term.Dim("Cancelled")
			return current
		}
		input = strings.TrimSpace(input)
		if input == "" {
			if current != "" {
				term.Dim("Test filter cleared")
			}
			return ""
		}
		term.Info("Test filter: %s", input)
		return input
	}

	term.Info("Discovered tests (%d) — type to filter:", len(allTests))

	prompt := term.Color(term.ColorCyan) + "> " + term.Color(term.ColorReset)
	onChange := func(input string) int {
		if input == "" {
			return renderTestList(allTests, "", traitInfo, 50, singleProject)
		}
		return renderTestList(allTests, input, traitInfo, 20, singleProject)
	}

	input, ok := kr.ReadLineFiltered(prompt, onChange)
	if !ok {
		term.Dim("Cancelled")
		return current
	}

	input = strings.TrimSpace(input)
	if input == "" {
		if current != "" {
			term.Dim("Test filter cleared")
		}
		return ""
	}

	// Check if input is a number selecting from the visible list
	if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(allTests) {
		selected := allTests[num-1].name
		term.Info("Test filter: %s", selected)
		return selected
	}

	term.Info("Test filter: %s", input)
	return input
}

// handleFilterTrait discovers traits across test projects and lets the user
// include or exclude a trait category. userFilter is the --filter value from
// the user's CLI args (used to show which traits are already filtered).
func handleFilterTrait(kr *term.KeyReader, projects []*project.Project, gitRoot string, current string, userFilter string) string {
	// Discover traits across all test projects
	allTraits := make(map[string]bool)
	for _, p := range projects {
		if !p.IsTest {
			continue
		}
		projectDir := filepath.Join(gitRoot, p.Dir)
		tm := testfilter.BuildTraitMap(projectDir)
		for _, t := range tm.AllTraits() {
			allTraits[t] = true
		}
	}

	if len(allTraits) == 0 {
		term.Warn("No traits found in test projects")
		return current
	}

	sorted := make([]string, 0, len(allTraits))
	for t := range allTraits {
		sorted = append(sorted, t)
	}
	sort.Strings(sorted)

	// Parse which traits are filtered by CLI args and by the current override
	cliIncluded, cliExcluded := parseCategoryFilters(userFilter)
	overrideIncluded, overrideExcluded := parseCategoryFilters(current)

	dim := term.Color(term.ColorDim)
	green := term.Color(term.ColorGreen)
	red := term.Color(term.ColorRed)
	yellow := term.Color(term.ColorYellow)
	reset := term.Color(term.ColorReset)

	term.Println()
	if current != "" {
		term.Dim("Current override: %s", current)
	}
	term.Info("Select trait (prefix with ! to exclude, empty to clear):")
	for i, t := range sorted {
		var marker string
		if overrideIncluded[t] {
			marker = green + " ✓ override" + reset
		} else if overrideExcluded[t] {
			marker = red + " ✗ override" + reset
		} else if cliIncluded[t] {
			marker = yellow + " ✓ cli" + reset
		} else if cliExcluded[t] {
			marker = dim + " ✗ cli" + reset
		}
		term.Printf("  %d) %s%s\n", i+1, t, marker)
	}

	input, ok := kr.ReadLine(term.Color(term.ColorCyan) + "> " + term.Color(term.ColorReset))
	if !ok {
		term.Dim("Cancelled")
		return current
	}

	input = strings.TrimSpace(input)
	if input == "" {
		if current != "" {
			term.Dim("Trait filter cleared")
		}
		return ""
	}

	exclude := false
	if strings.HasPrefix(input, "!") {
		exclude = true
		input = strings.TrimPrefix(input, "!")
		input = strings.TrimSpace(input)
	}

	// Try to parse as number
	var traitName string
	if num, err := strconv.Atoi(input); err == nil && num >= 1 && num <= len(sorted) {
		traitName = sorted[num-1]
	} else {
		// Use as literal trait name
		traitName = input
	}

	if exclude {
		expr := "Category!=" + traitName
		term.Info("Trait filter: %s", expr)
		return expr
	}

	expr := "Category=" + traitName
	term.Info("Trait filter: %s", expr)
	return expr
}
