// Package config handles configuration loading from files and environment.
package config

import "runtime"

// Config holds all donotnet configuration settings.
// Config is merged from multiple sources: user config, parent directories,
// git root, current directory, environment variables, and command-line flags.
type Config struct {
	// Global settings
	Verbose      bool   `koanf:"verbose"`
	Parallel     int    `koanf:"parallel"`     // 0 = auto (GOMAXPROCS)
	Color        string `koanf:"color"`        // auto, always, never
	ShowCached   bool   `koanf:"show_cached"`
	Local        bool   `koanf:"local"`
	KeepGoing    bool   `koanf:"keep_going"`
	Quiet        bool   `koanf:"quiet"`
	NoProgress   bool   `koanf:"no_progress"`
	NoSuggestions bool  `koanf:"no_suggestions"`
	CacheDir     string `koanf:"cache_dir"`

	Test  TestConfig  `koanf:"test"`
	Build BuildConfig `koanf:"build"`
	VCS   VCSConfig   `koanf:"vcs"`
	Watch WatchConfig `koanf:"watch"`
}

// TestConfig holds test command settings.
type TestConfig struct {
	Heuristics          string `koanf:"heuristics"`           // default, none, or comma-separated
	Coverage            bool   `koanf:"coverage"`
	CoverageGranularity string `koanf:"coverage_granularity"` // method, class, file
	StalenessCheck      string `koanf:"staleness_check"`      // git, mtime, both
	Reports             bool   `koanf:"reports"`
	Failed              bool   `koanf:"failed"`
}

// BuildConfig holds build command settings.
type BuildConfig struct {
	Solution  string `koanf:"solution"`   // auto, always, never
	FullBuild bool   `koanf:"full_build"`
}

// VCSConfig holds version control settings.
type VCSConfig struct {
	Ref     string `koanf:"ref"`
	Changed bool   `koanf:"changed"`
}

// WatchConfig holds watch mode settings.
type WatchConfig struct {
	DebounceMs int `koanf:"debounce_ms"`
}

// Default returns the default configuration.
func Default() *Config {
	return &Config{
		Verbose:       false,
		Parallel:      0, // auto = GOMAXPROCS
		Color:         "auto",
		ShowCached:    false,
		Local:         false,
		KeepGoing:     false,
		Quiet:         false,
		NoProgress:    false,
		NoSuggestions: false,
		CacheDir:      "",

		Test: TestConfig{
			Heuristics:          "default",
			Coverage:            false,
			CoverageGranularity: "class",
			StalenessCheck:      "git",
			Reports:             true,
			Failed:              false,
		},

		Build: BuildConfig{
			Solution:  "auto",
			FullBuild: false,
		},

		VCS: VCSConfig{
			Ref:     "",
			Changed: false,
		},

		Watch: WatchConfig{
			DebounceMs: 100,
		},
	}
}

// EffectiveParallel returns the actual parallelism to use.
// If Parallel is 0 (auto), it returns GOMAXPROCS.
func (c *Config) EffectiveParallel() int {
	if c.Parallel <= 0 {
		return runtime.GOMAXPROCS(0)
	}
	return c.Parallel
}
