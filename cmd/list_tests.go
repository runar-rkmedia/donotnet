package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
	"github.com/spf13/cobra"
)

var (
	listTestsJSON     bool
	listTestsAffected bool
)

// testDetail represents a single test with optional trait info.
type testDetail struct {
	Name   string   `json:"name"`
	Traits []string `json:"traits,omitempty"`
}

// testListEntry represents a project and its discovered tests.
type testListEntry struct {
	Project string       `json:"project"`
	Tests   []testDetail `json:"tests"`
	Cached  bool         `json:"cached,omitempty"`
}

var listTestsCmd = &cobra.Command{
	Use:   "tests",
	Short: "List all tests in affected test projects",
	Long: `List all tests discovered in test projects.

Runs 'dotnet test --list-tests' on each test project and collects test names.
Results are cached based on source file content hashes.
By default outputs JSON. Use --json=false for plain text output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scan, err := scanProjects()
		if err != nil {
			return err
		}

		// Open cache
		cachePath, err := getCachePath()
		if err != nil {
			return err
		}
		db, err := cache.Open(cachePath)
		if err != nil {
			return err
		}
		defer db.Close()

		argsHash := runner.HashArgs([]string{"list-tests"})

		// When --affected is set, scope to affected projects only
		var affectedSet map[string]bool
		if listTestsAffected {
			vcsChangedFiles := git.GetDirtyFiles(scan.GitRoot)
			useVcsFilter := len(vcsChangedFiles) > 0
			testArgsHash := runner.HashArgs([]string{"test"})

			changed := make(map[string]bool)
			for _, p := range scan.Projects {
				relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
				if useVcsFilter {
					projectVcsFiles := project.FilterFilesToProject(vcsChangedFiles, relevantDirs)
					if len(projectVcsFiles) == 0 {
						continue
					}
				}
				contentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
				key := cache.MakeKey(contentHash, testArgsHash, p.Path)
				if flagForce || db.Lookup(key) == nil {
					changed[p.Path] = true
				}
			}
			affectedSet = project.FindAffectedProjects(changed, scan.Graph, scan.Projects)
			term.Verbose("Affected projects: %d", len(affectedSet))
		}

		// Filter to test projects only
		var testProjects []*testListEntry
		for _, p := range scan.Projects {
			if !p.IsTest {
				continue
			}
			if affectedSet != nil && !affectedSet[p.Path] {
				continue
			}

			// Check cache
			relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
			contentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)

			var testNames []string
			cached := false

			if !flagForce {
				if result := db.Lookup(key); result != nil && len(result.Output) > 0 {
					names := strings.Split(strings.TrimSpace(string(result.Output)), "\n")
					if len(names) > 0 && names[0] != "" {
						term.Verbose("  cache hit: %s (%d tests)", p.Name, len(names))
						testNames = names
						cached = true
					}
				}
			}

			if testNames == nil {
				// Cache miss - run dotnet test --list-tests
				term.Verbose("  cache miss: %s", p.Name)
				projectPath := filepath.Join(scan.GitRoot, p.Path)
				names, listErr := coverage.ListTests(cmd.Context(), scan.GitRoot, projectPath)
				if listErr != nil {
					term.Warnf("Failed to list tests for %s: %v", p.Name, listErr)
					continue
				}

				// Store in cache
				output := strings.Join(names, "\n")
				db.Mark(key, time.Now(), true, []byte(output), "list-tests")
				testNames = names
			}

			// Build trait map for this project
			projectPath := filepath.Join(scan.GitRoot, p.Path)
			projectDir := filepath.Dir(projectPath)
			traitMap := testfilter.BuildTraitMap(projectDir)

			// Build detailed test list with traits
			details := make([]testDetail, 0, len(testNames))
			for _, t := range testNames {
				traits := traitMap.GetTraitsForTest(t)
				details = append(details, testDetail{Name: t, Traits: traits})
			}

			testProjects = append(testProjects, &testListEntry{
				Project: p.Name,
				Tests:   details,
				Cached:  cached,
			})
		}

		if len(testProjects) == 0 {
			term.Dim("No test projects found")
			return nil
		}

		if listTestsJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(testProjects)
		}

		// Plain text output
		for _, entry := range testProjects {
			suffix := ""
			if entry.Cached {
				suffix = " (cached)"
			}
			term.Printf("%s (%d tests)%s\n", entry.Project, len(entry.Tests), suffix)
			for _, t := range entry.Tests {
				if len(t.Traits) > 0 {
					term.Printf("  %s %s[%s]%s\n", t.Name,
						term.Color(term.ColorYellow), strings.Join(t.Traits, ", "), term.Color(term.ColorReset))
				} else {
					term.Printf("  %s\n", t.Name)
				}
			}
		}
		return nil
	},
}

func init() {
	listTestsCmd.Flags().BoolVar(&listTestsJSON, "json", true, "Output as JSON")
	listTestsCmd.Flags().BoolVar(&listTestsAffected, "affected", false, "Only list tests from affected projects (VCS-changed + cache miss)")
	listCmd.AddCommand(listTestsCmd)
}

