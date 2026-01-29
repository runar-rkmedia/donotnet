package cmd

import (
	"os"

	"github.com/runar-rkmedia/donotnet/devplan"
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

		// Convert to devplan projects
		var planProjects []*devplan.Project
		for _, p := range scan.Projects {
			planProjects = append(planProjects, &devplan.Project{
				Path: p.Path,
				Name: p.Name,
			})
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
