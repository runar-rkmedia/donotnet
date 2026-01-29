// Package runner provides the core execution logic for donotnet commands.
package runner

import (
	"github.com/runar-rkmedia/donotnet/config"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// TestFilterer provides test filtering capabilities.
// This interface allows the runner to use test filters without importing
// the concrete TestFilter type from package main.
type TestFilterer interface {
	GetFilter(projectPath string, gitRoot string, userFilter string) testfilter.FilterResult
}

// Options contains all options for running a test or build command.
type Options struct {
	// Command is "test" or "build"
	Command string

	// DotnetArgs are extra arguments passed to dotnet after "--"
	DotnetArgs []string

	// --- Test-specific options ---
	Coverage            bool
	CoverageBuild       bool // Per-test coverage map build (donotnet coverage build)
	Heuristics          string
	Failed              bool
	StalenessCheck      string
	CoverageGranularity string
	NoReports           bool

	// --- Build-specific options ---
	FullBuild     bool
	NoSolution    bool
	ForceSolution bool

	// --- Shared options ---
	VcsChanged  bool
	VcsRef      string
	Watch       bool
	PrintOutput bool
	Force       bool

	// --- Global options ---
	Verbose       bool
	Quiet         bool
	Parallel      int
	Local         bool
	KeepGoing     bool
	ShowCached    bool
	NoProgress    bool
	NoSuggestions bool
	CacheDir      string

	// Config from file/env (used for defaults)
	Config *config.Config

	// TestFilter provides optional test filtering (set by caller)
	TestFilter TestFilterer

	// FailedTestFilters maps project path -> filter expression for --failed mode
	FailedTestFilters map[string]string

	// BuildOnlyProjects maps project paths that should only be built, not tested
	BuildOnlyProjects map[string]bool
}

// NewOptions creates Options with defaults from config.
func NewOptions(cfg *config.Config) *Options {
	opts := &Options{
		Config: cfg,
	}

	if cfg != nil {
		opts.Verbose = cfg.Verbose
		opts.Quiet = cfg.Quiet
		opts.Parallel = cfg.Parallel
		opts.Local = cfg.Local
		opts.KeepGoing = cfg.KeepGoing
		opts.ShowCached = cfg.ShowCached
		opts.NoProgress = cfg.NoProgress
		opts.NoSuggestions = cfg.NoSuggestions
		opts.CacheDir = cfg.CacheDir

		// Test defaults
		opts.Heuristics = cfg.Test.Heuristics
		opts.Coverage = cfg.Test.Coverage
		opts.CoverageGranularity = cfg.Test.CoverageGranularity
		opts.StalenessCheck = cfg.Test.StalenessCheck
		opts.NoReports = !cfg.Test.Reports
		opts.Failed = cfg.Test.Failed

		// Build defaults
		opts.FullBuild = cfg.Build.FullBuild
		if cfg.Build.Solution == "never" {
			opts.NoSolution = true
		} else if cfg.Build.Solution == "always" {
			opts.ForceSolution = true
		}

		// VCS defaults
		opts.VcsRef = cfg.VCS.Ref
		opts.VcsChanged = cfg.VCS.Changed
	} else {
		// Sensible defaults without config
		opts.Heuristics = "default"
		opts.CoverageGranularity = "class"
		opts.StalenessCheck = "git"
	}

	return opts
}

// EffectiveParallel returns the parallelism to use.
func (o *Options) EffectiveParallel() int {
	if o.Parallel <= 0 {
		if o.Config != nil {
			return o.Config.EffectiveParallel()
		}
		return 0 // Will be set to GOMAXPROCS by runner
	}
	return o.Parallel
}
