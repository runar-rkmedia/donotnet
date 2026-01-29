package cmd

import (
	"os"
	"path/filepath"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/devplan"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show job scheduling plan",
	Long: `Show the job scheduling plan based on project dependencies.

Displays how projects would be scheduled in parallel waves based on
their dependency relationships. Useful for understanding build order
and identifying potential bottlenecks.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scan, err := scanProjects()
		if err != nil {
			return err
		}

		if len(scan.Projects) == 0 {
			term.Dim("No projects found")
			return nil
		}

		// Filter to affected projects (cache-miss + their dependents)
		// matching old behavior that combined targetProjects and cachedProjects.
		cachePath, err := getCachePath()
		if err != nil {
			return err
		}
		cacheDir := filepath.Dir(cachePath)
		os.MkdirAll(cacheDir, 0755)

		db, err := cache.Open(cachePath)
		if err != nil {
			return err
		}
		defer db.Close()

		argsHash := runner.HashArgs([]string{"test"})
		changed := make(map[string]bool)
		for _, p := range scan.Projects {
			relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
			contentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			if db.Lookup(key) == nil {
				changed[p.Path] = true
			}
		}

		affected := project.FindAffectedProjects(changed, scan.Graph, scan.Projects)

		// Convert to devplan projects (only affected)
		var planProjects []*devplan.Project
		for _, p := range scan.Projects {
			if !affected[p.Path] {
				continue
			}
			planProjects = append(planProjects, &devplan.Project{
				Path: p.Path,
				Name: p.Name,
			})
		}

		if len(planProjects) == 0 {
			term.Dim("All projects are cached, nothing to run")
			return nil
		}

		plan := devplan.ComputePlan(planProjects, scan.ForwardGraph)

		colors := devplan.DefaultColors()
		if term.IsPlain() {
			colors = devplan.PlainColors()
		}
		plan.Print(os.Stdout, colors)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(planCmd)
}
