// Package cmd implements the CLI commands for donotnet.
package cmd

import (
	"fmt"
	"os"

	"github.com/runar-rkmedia/donotnet/config"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	flagVerbose       bool
	flagQuiet         bool
	flagColor         string
	flagDir           string
	flagCacheDir      string
	flagParallel      int
	flagLocal         bool
	flagKeepGoing     bool
	flagNoProgress    bool
	flagNoSuggestions bool
	flagShowCached    bool
	flagConfigFile    string
	flagForce         bool

	// Loaded configuration
	cfg *config.Config
)

// rootCmd is the base command when called without subcommands.
var rootCmd = &cobra.Command{
	Use:   "donotnet",
	Short: "Fast affected project detection for .NET",
	Long: `donotnet - Fast affected project detection for .NET

Do not run what you don't need to. Tracks file changes to determine which
projects need rebuilding/retesting. Uses git-aware caching for speed and
accuracy across branches and stashes.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Handle -C flag (change directory)
		if flagDir != "" {
			if err := os.Chdir(flagDir); err != nil {
				return fmt.Errorf("changing to directory %s: %w", flagDir, err)
			}
		}

		// Get current working directory
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}

		// Find git root (may be empty if not in a git repo)
		gitRoot, _ := git.FindRootFrom(cwd)

		// Load configuration
		result, err := config.Load(config.LoadOptions{
			CWD:        cwd,
			GitRoot:    gitRoot,
			ConfigFile: flagConfigFile,
			Verbose:    flagVerbose,
		})
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		cfg = result.Config

		// Apply flag overrides to config
		applyFlagOverrides()

		// Initialize terminal settings
		term.SetVerbose(cfg.Verbose)
		term.SetQuiet(cfg.Quiet)

		switch cfg.Color {
		case "always":
			term.SetColorMode(term.ColorModeAlways)
		case "never":
			term.SetColorMode(term.ColorModeNever)
		default:
			term.SetColorMode(term.ColorModeAuto)
		}

		return nil
	},
	// Silence usage on errors (we handle our own error messages)
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Quiet mode - suppress progress output")
	rootCmd.PersistentFlags().StringVar(&flagColor, "color", "", "Color output mode: auto, always, never")
	rootCmd.PersistentFlags().StringVarP(&flagDir, "dir", "C", "", "Change to directory before running")
	rootCmd.PersistentFlags().StringVar(&flagCacheDir, "cache-dir", "", "Cache directory path")
	rootCmd.PersistentFlags().IntVarP(&flagParallel, "parallel", "j", 0, "Number of parallel workers (0 = auto)")
	rootCmd.PersistentFlags().BoolVar(&flagLocal, "local", false, "Only scan current directory, not entire git repo")
	rootCmd.PersistentFlags().BoolVarP(&flagKeepGoing, "keep-going", "k", false, "Keep going on errors")
	rootCmd.PersistentFlags().BoolVar(&flagNoProgress, "no-progress", false, "Disable progress output")
	rootCmd.PersistentFlags().BoolVar(&flagNoSuggestions, "no-suggestions", false, "Disable performance suggestions")
	rootCmd.PersistentFlags().BoolVar(&flagShowCached, "show-cached", false, "Show cached projects in output")
	rootCmd.PersistentFlags().StringVar(&flagConfigFile, "config", "", "Config file path (overrides auto-discovery)")
	rootCmd.PersistentFlags().BoolVar(&flagForce, "force", false, "Ignore cache, run all projects")
}

// applyFlagOverrides applies command-line flag values to the config.
// Flags only override if they were explicitly set.
func applyFlagOverrides() {
	if flagVerbose {
		cfg.Verbose = true
	}
	if flagQuiet {
		cfg.Quiet = true
	}
	if flagColor != "" {
		cfg.Color = flagColor
	}
	if flagCacheDir != "" {
		cfg.CacheDir = flagCacheDir
	}
	if flagParallel != 0 {
		cfg.Parallel = flagParallel
	}
	if flagLocal {
		cfg.Local = true
	}
	if flagKeepGoing {
		cfg.KeepGoing = true
	}
	if flagNoProgress {
		cfg.NoProgress = true
	}
	if flagNoSuggestions {
		cfg.NoSuggestions = true
	}
	if flagShowCached {
		cfg.ShowCached = true
	}
}

// GetConfig returns the loaded configuration.
// Must be called after PersistentPreRunE has executed.
func GetConfig() *config.Config {
	return cfg
}

// IsForce returns whether the force flag was set.
func IsForce() bool {
	return flagForce
}
