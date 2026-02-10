package cmd

import (
	"github.com/spf13/cobra"
)

var (
	// Build-specific flags
	buildFlagNoSolution   bool
	buildFlagSolution     bool
	buildFlagFullBuild    bool
	buildFlagVcsChanged   bool
	buildFlagVcsRef       string
	buildFlagWatch        bool
	buildFlagPrintOutput  bool

	// Mapped dotnet flags
	buildFlagConfiguration string
)

var buildCmd = &cobra.Command{
	Use:   "build [path...] [flags] [-- extra-dotnet-args...]",
	Short: "Build affected projects",
	Long: `Run 'dotnet build' on affected projects.

Only projects with changes (or dependencies with changes) are built.
Results are cached for fast subsequent runs.

Paths can be .csproj files, .sln files, or directories to scope the run.
Explicit targets bypass the cache (force run).

Examples:
  donotnet build                         Build affected projects
  donotnet build path/to/Bar.csproj      Build a specific project
  donotnet build src/FeatureX/           Build all projects under a directory
  donotnet build -c Release              Build in Release configuration
  donotnet build -- --no-restore         Pass extra args to dotnet
  donotnet build --vcs-changed           Build projects with uncommitted changes
  donotnet build --vcs-ref=main          Build projects changed vs main branch
  donotnet build --watch                 Watch for changes and rebuild`,
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

	// Mapped dotnet flags (no -- needed)
	buildCmd.Flags().StringVarP(&buildFlagConfiguration, "configuration", "c", "", "Build configuration (e.g. Debug, Release)")

	rootCmd.AddCommand(buildCmd)
}

func runBuild(cmd *cobra.Command, args []string) error {
	// Split positional args (paths) from passthrough args (after --)
	paths, dotnetArgs := splitArgsAtDash(cmd, args)

	// Check for misplaced dotnet filter expressions in positional args
	if err := checkForMisplacedDotnetArgs("build", paths, dotnetArgs); err != nil {
		return err
	}

	// Resolve path targets
	targets, err := resolveTargets(paths)
	if err != nil {
		return err
	}

	// Inject mapped flags into dotnet args
	dotnetArgs = injectMappedFlags(dotnetArgs, "", buildFlagConfiguration)

	// Build options from flags
	opts := &RunOptions{
		Command:       "build",
		DotnetArgs:    dotnetArgs,
		Targets:       targets,
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
