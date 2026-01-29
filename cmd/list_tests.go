package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	listTestsJSON bool
)

// testListEntry represents a project and its discovered tests.
type testListEntry struct {
	Project string   `json:"project"`
	Tests   []string `json:"tests"`
	Cached  bool     `json:"cached,omitempty"`
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

		// Filter to test projects only
		var testProjects []*testListEntry
		for _, p := range scan.Projects {
			if !p.IsTest {
				continue
			}

			// Check cache
			relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
			contentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)

			if !flagForce {
				if result := db.Lookup(key); result != nil && len(result.Output) > 0 {
					tests := strings.Split(strings.TrimSpace(string(result.Output)), "\n")
					if len(tests) > 0 && tests[0] != "" {
						term.Verbose("  cache hit: %s (%d tests)", p.Name, len(tests))
						testProjects = append(testProjects, &testListEntry{
							Project: p.Name,
							Tests:   tests,
							Cached:  true,
						})
						continue
					}
				}
			}

			// Cache miss - run dotnet test --list-tests
			term.Verbose("  cache miss: %s", p.Name)
			projectPath := filepath.Join(scan.GitRoot, p.Path)
			tests, listErr := listProjectTests(cmd.Context(), projectPath)
			if listErr != nil {
				term.Warnf("Failed to list tests for %s: %v", p.Name, listErr)
				continue
			}

			// Store in cache
			output := strings.Join(tests, "\n")
			db.Mark(key, time.Now(), true, []byte(output), "list-tests")

			testProjects = append(testProjects, &testListEntry{
				Project: p.Name,
				Tests:   tests,
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
				term.Printf("  %s\n", t)
			}
		}
		return nil
	},
}

func init() {
	listTestsCmd.Flags().BoolVar(&listTestsJSON, "json", true, "Output as JSON")
	listCmd.AddCommand(listTestsCmd)
}

// listProjectTests runs 'dotnet test --list-tests --no-build' on a project
// and parses the output to extract test names.
func listProjectTests(ctx context.Context, projectPath string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "dotnet", "test", projectPath, "--list-tests", "--no-build")
	output, err := cmd.Output()
	if err != nil {
		// Try again with build
		cmd = exec.CommandContext(ctx, "dotnet", "test", projectPath, "--list-tests")
		output, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("dotnet test --list-tests: %w", err)
		}
	}

	var tests []string
	inTestList := false
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "The following Tests are available:") {
			inTestList = true
			continue
		}
		if inTestList && line != "" && !strings.HasPrefix(line, "Test run") {
			tests = append(tests, line)
		}
	}

	return tests, nil
}
