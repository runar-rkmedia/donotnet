package cmd

import (
	"context"

	"github.com/runar-rkmedia/donotnet/config"
	"github.com/runar-rkmedia/donotnet/runner"
)

// RunOptions contains all options for running a test or build command.
// This is a bridge type that maps to runner.Options.
type RunOptions struct {
	// Command is "test" or "build"
	Command string

	// DotnetArgs are extra arguments passed to dotnet
	DotnetArgs []string

	// Targets are resolved absolute paths to specific .csproj, .sln, or directories
	Targets []string

	// Test-specific options
	Coverage            bool
	CoverageBuild       bool
	Heuristics          string
	Failed              bool
	StalenessCheck      string
	CoverageGranularity string
	NoReports           bool

	// Build-specific options
	FullBuild     bool
	NoSolution    bool
	ForceSolution bool

	// Shared options
	VcsChanged  bool
	VcsRef      string
	Watch       bool
	PrintOutput bool
	Force       bool

	// Config from file/env
	Config *config.Config
}

// Run executes the test or build command with the given options.
func Run(opts *RunOptions) error {
	// Convert to runner options
	runnerOpts := runner.NewOptions(opts.Config)

	// Override with command-specific options
	runnerOpts.Command = opts.Command
	runnerOpts.DotnetArgs = opts.DotnetArgs
	runnerOpts.Targets = opts.Targets
	runnerOpts.Force = opts.Force

	// Test options
	if opts.Coverage {
		runnerOpts.Coverage = true
	}
	if opts.CoverageBuild {
		runnerOpts.CoverageBuild = true
	}
	if opts.Heuristics != "" {
		runnerOpts.Heuristics = opts.Heuristics
	}
	if opts.Failed {
		runnerOpts.Failed = true
	}
	if opts.StalenessCheck != "" {
		runnerOpts.StalenessCheck = opts.StalenessCheck
	}
	if opts.CoverageGranularity != "" {
		runnerOpts.CoverageGranularity = opts.CoverageGranularity
	}
	if opts.NoReports {
		runnerOpts.NoReports = true
	}

	// Build options
	if opts.FullBuild {
		runnerOpts.FullBuild = true
	}
	if opts.NoSolution {
		runnerOpts.NoSolution = true
	}
	if opts.ForceSolution {
		runnerOpts.ForceSolution = true
	}

	// Shared options
	if opts.VcsChanged {
		runnerOpts.VcsChanged = true
	}
	if opts.VcsRef != "" {
		runnerOpts.VcsRef = opts.VcsRef
	}
	if opts.Watch {
		runnerOpts.Watch = true
	}
	if opts.PrintOutput {
		runnerOpts.PrintOutput = true
	}

	// Create and run
	r := runner.New(runnerOpts)
	return r.Run(context.Background())
}
