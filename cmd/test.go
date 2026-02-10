package cmd

import (
	"github.com/spf13/cobra"
)

var (
	// Test-specific flags
	testFlagCoverage            bool
	testFlagHeuristics          string
	testFlagFailed              bool
	testFlagStalenessCheck      string
	testFlagCoverageGranularity string
	testFlagNoReports           bool
	testFlagVcsChanged          bool
	testFlagVcsRef              string
	testFlagWatch               bool
	testFlagPrintOutput         bool
	testFlagFullBuild           bool
	testFlagNoSolution          bool
	testFlagSolution            bool

	// Mapped dotnet flags
	testFlagFilter        string
	testFlagConfiguration string
)

var testCmd = &cobra.Command{
	Use:   "test [path...] [flags] [-- extra-dotnet-args...]",
	Short: "Run tests on affected test projects",
	Long: `Run 'dotnet test' on affected test projects.

Only projects with changes (or dependencies with changes) are tested.
Results are cached for fast subsequent runs.

Paths can be .csproj files, .sln files, or directories to scope the run.
Explicit targets bypass the cache (force run).

Examples:
  donotnet test                           Run tests on affected projects
  donotnet test path/to/Foo.Tests.csproj  Test a specific project
  donotnet test src/FeatureX/             Test all projects under a directory
  donotnet test --filter "Name~Foo"       Run with a dotnet test filter
  donotnet test -c Release                Test in Release configuration
  donotnet test -- --no-build             Pass extra args to dotnet
  donotnet test --coverage                Collect code coverage
  donotnet test --failed                  Rerun only failed tests
  donotnet test --watch                   Watch for changes and rerun
  donotnet test --vcs-changed             Test projects with uncommitted changes
  donotnet test --vcs-ref=main            Test projects changed vs main branch`,
	RunE: runTest,
}

func init() {
	// Test-specific flags
	testCmd.Flags().BoolVar(&testFlagCoverage, "coverage", false, "Collect code coverage during test runs")
	testCmd.Flags().StringVar(&testFlagHeuristics, "heuristics", "default", "Test filter heuristics: default, none, or comma-separated names")
	testCmd.Flags().BoolVar(&testFlagFailed, "failed", false, "Only run previously failed tests")
	testCmd.Flags().StringVar(&testFlagStalenessCheck, "staleness-check", "git", "Coverage staleness check method: git, mtime, both")
	testCmd.Flags().StringVar(&testFlagCoverageGranularity, "coverage-granularity", "class", "Coverage granularity: method, class, file")
	testCmd.Flags().BoolVar(&testFlagNoReports, "no-reports", false, "Disable saving test reports (TRX files)")

	// Shared test/build flags
	testCmd.Flags().BoolVar(&testFlagVcsChanged, "vcs-changed", false, "Only test projects with uncommitted changes")
	testCmd.Flags().StringVar(&testFlagVcsRef, "vcs-ref", "", "Only test projects changed vs specified ref")
	testCmd.Flags().BoolVar(&testFlagWatch, "watch", false, "Watch for file changes and rerun")
	testCmd.Flags().BoolVar(&testFlagPrintOutput, "print-output", false, "Print stdout from all projects after completion")
	testCmd.Flags().BoolVar(&testFlagFullBuild, "full-build", false, "Disable auto --no-build detection")
	testCmd.Flags().BoolVar(&testFlagNoSolution, "no-solution", false, "Disable solution-level builds")
	testCmd.Flags().BoolVar(&testFlagSolution, "solution", false, "Force solution-level builds")

	// Mapped dotnet flags (no -- needed)
	testCmd.Flags().StringVar(&testFlagFilter, "filter", "", "Dotnet test filter expression (e.g. \"Name~Foo\")")
	testCmd.Flags().StringVarP(&testFlagConfiguration, "configuration", "c", "", "Build configuration (e.g. Debug, Release)")

	rootCmd.AddCommand(testCmd)
}

func runTest(cmd *cobra.Command, args []string) error {
	// Split positional args (paths) from passthrough args (after --)
	paths, dotnetArgs := splitArgsAtDash(cmd, args)

	// Check for --filter conflict (native flag vs passthrough)
	if err := checkFilterConflict(testFlagFilter, dotnetArgs); err != nil {
		return err
	}

	// Check for misplaced dotnet filter expressions in positional args
	if err := checkForMisplacedDotnetArgs("test", paths, dotnetArgs); err != nil {
		return err
	}

	// Resolve path targets
	targets, err := resolveTargets(paths)
	if err != nil {
		return err
	}

	// Inject mapped flags into dotnet args
	dotnetArgs = injectMappedFlags(dotnetArgs, testFlagFilter, testFlagConfiguration)

	// Build options from flags
	opts := &RunOptions{
		Command:             "test",
		DotnetArgs:          dotnetArgs,
		Targets:             targets,
		Coverage:            testFlagCoverage,
		Heuristics:          testFlagHeuristics,
		Failed:              testFlagFailed,
		StalenessCheck:      testFlagStalenessCheck,
		CoverageGranularity: testFlagCoverageGranularity,
		NoReports:           testFlagNoReports,
		VcsChanged:          testFlagVcsChanged,
		VcsRef:              testFlagVcsRef,
		Watch:               testFlagWatch,
		PrintOutput:         testFlagPrintOutput,
		FullBuild:           testFlagFullBuild,
		NoSolution:          testFlagNoSolution,
		ForceSolution:       testFlagSolution,
		Force:               IsForce(),
		Config:              GetConfig(),
	}

	return Run(opts)
}
