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
)

var testCmd = &cobra.Command{
	Use:   "test [flags] [-- dotnet-args...]",
	Short: "Run tests on affected test projects",
	Long: `Run 'dotnet test' on affected test projects.

Only projects with changes (or dependencies with changes) are tested.
Results are cached for fast subsequent runs.

Examples:
  donotnet test                     Run tests on affected projects
  donotnet test -- --no-build       Run tests without building
  donotnet test --coverage          Collect code coverage
  donotnet test --failed            Rerun only failed tests
  donotnet test --watch             Watch for changes and rerun
  donotnet test --vcs-changed       Test projects with uncommitted changes
  donotnet test --vcs-ref=main      Test projects changed vs main branch`,
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

	rootCmd.AddCommand(testCmd)
}

func runTest(cmd *cobra.Command, args []string) error {
	// Check for misplaced dotnet filter expressions
	if err := checkForMisplacedDotnetArgs("test", cmd.Flags().Args(), args); err != nil {
		return err
	}

	// Extract dotnet args after "--"
	dotnetArgs := args

	// Build options from flags
	opts := &RunOptions{
		Command:             "test",
		DotnetArgs:          dotnetArgs,
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
