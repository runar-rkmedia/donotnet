package cmd

import (
	"github.com/spf13/cobra"
)

var (
	coverageBuildGranularity string
)

var coverageBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build per-test coverage map",
	Long: `Build per-test coverage map for test projects.

This runs tests with code coverage collection enabled. Equivalent to
running 'donotnet test --coverage'.

Granularity levels:
  method - Most precise, collects per-method coverage
  class  - Collects per-class coverage (default, good balance)
  file   - Fastest, collects per-file coverage`,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := &RunOptions{
			Command:             "test",
			CoverageBuild:       true,
			CoverageGranularity: coverageBuildGranularity,
			Force:               IsForce(),
			Config:              GetConfig(),
		}
		return Run(opts)
	},
}

func init() {
	coverageBuildCmd.Flags().StringVar(&coverageBuildGranularity, "granularity", "class", "Coverage granularity: method, class, file")
	coverageCmd.AddCommand(coverageBuildCmd)
}
