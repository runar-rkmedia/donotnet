package cmd

import (
	"github.com/spf13/cobra"
)

var (
	// Build-specific flags
	buildFlagNoSolution bool
	buildFlagSolution   bool
	buildFlagFullBuild  bool
	buildFlagVcsChanged bool
	buildFlagVcsRef     string
	buildFlagWatch      bool
	buildFlagPrintOutput bool
)

var buildCmd = &cobra.Command{
	Use:   "build [flags] [-- dotnet-args...]",
	Short: "Build affected projects",
	Long: `Run 'dotnet build' on affected projects.

Only projects with changes (or dependencies with changes) are built.
Results are cached for fast subsequent runs.

Examples:
  donotnet build                    Build affected projects
  donotnet build -- -c Release      Build in Release configuration
  donotnet build --vcs-changed      Build projects with uncommitted changes
  donotnet build --vcs-ref=main     Build projects changed vs main branch
  donotnet build --watch            Watch for changes and rebuild`,
	RunE: runBuild,
}

func init() {
	// Build-specific flags
	buildCmd.Flags().BoolVar(&buildFlagNoSolution, "no-solution", false, "Disable solution-level builds")
	buildCmd.Flags().BoolVar(&buildFlagSolution, "solution", false, "Force solution-level builds")
	buildCmd.Flags().BoolVar(&buildFlagFullBuild, "full-build", false, "Disable auto --no-restore detection")

	// Shared test/build flags
	buildCmd.Flags().BoolVar(&buildFlagVcsChanged, "vcs-changed", false, "Only build projects with uncommitted changes")
	buildCmd.Flags().StringVar(&buildFlagVcsRef, "vcs-ref", "", "Only build projects changed vs specified ref")
	buildCmd.Flags().BoolVar(&buildFlagWatch, "watch", false, "Watch for file changes and rebuild")
	buildCmd.Flags().BoolVar(&buildFlagPrintOutput, "print-output", false, "Print stdout from all projects after completion")

	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	// Check for misplaced dotnet filter expressions
	if err := checkForMisplacedDotnetArgs("build", cmd.Flags().Args()); err != nil {
		return err
	}

	// Extract dotnet args after "--"
	dotnetArgs := args

	// Build options from flags
	opts := &RunOptions{
		Command:       "build",
		DotnetArgs:    dotnetArgs,
		VcsChanged:    buildFlagVcsChanged,
		VcsRef:        buildFlagVcsRef,
		Watch:         buildFlagWatch,
		PrintOutput:   buildFlagPrintOutput,
		FullBuild:     buildFlagFullBuild,
		NoSolution:    buildFlagNoSolution,
		ForceSolution: buildFlagSolution,
		Force:         IsForce(),
		Config:        GetConfig(),
	}

	return Run(opts)
}
