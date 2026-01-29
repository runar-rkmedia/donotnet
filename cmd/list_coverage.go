package cmd

import (
	"path/filepath"
	"sort"

	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	listCoverageGranularity string
)

var listCoverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "List test groupings for coverage granularity levels",
	Long: `List test groupings based on existing coverage data.

Shows which test projects cover which source files, based on
previously collected coverage data. Run 'donotnet coverage build'
first to generate coverage data.

Granularity levels:
  method - Most precise, groups by individual test methods
  class  - Groups by test class (default, good balance)
  file   - Fastest, groups by source file`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scan, err := scanProjects()
		if err != nil {
			return err
		}

		// Build coverage map from existing coverage data
		var testProjects []coverage.TestProject
		for _, p := range scan.Projects {
			if !p.IsTest {
				continue
			}
			projectDir := filepath.Dir(p.Path)
			testProjects = append(testProjects, coverage.TestProject{
				Path: p.Path,
				Dir:  projectDir,
			})
		}

		if len(testProjects) == 0 {
			term.Dim("No test projects found")
			return nil
		}

		covMap := coverage.BuildMap(scan.GitRoot, testProjects)

		if !covMap.HasCoverage() {
			term.Println("No coverage data found. Run 'donotnet coverage build' to collect coverage.")
			return nil
		}

		// Show coverage map grouped by test project
		term.Printf("Coverage map (granularity: %s)\n\n", listCoverageGranularity)

		for _, tp := range testProjects {
			files := covMap.TestProjectToFiles[tp.Path]
			if len(files) == 0 {
				continue
			}
			sort.Strings(files)
			name := filepath.Base(filepath.Dir(tp.Path))
			term.Printf("%s (%d files covered)\n", name, len(files))
			for _, f := range files {
				term.Printf("  %s\n", f)
			}
			term.Println()
		}

		// Show stale/missing
		if len(covMap.StaleTestProjects) > 0 {
			term.Warnf("Stale coverage data:")
			for _, p := range covMap.StaleTestProjects {
				term.Printf("  %s\n", filepath.Base(filepath.Dir(p)))
			}
		}
		if len(covMap.MissingTestProjects) > 0 {
			term.Warnf("Missing coverage data:")
			for _, p := range covMap.MissingTestProjects {
				term.Printf("  %s\n", filepath.Base(filepath.Dir(p)))
			}
		}

		return nil
	},
}

func init() {
	listCoverageCmd.Flags().StringVar(&listCoverageGranularity, "granularity", "class", "Coverage granularity: method, class, file")
	listCmd.AddCommand(listCoverageCmd)
}
