package cmd

import (
	"os"
	"path/filepath"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var cacheStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show cache statistics",
	Long:  `Display statistics about the donotnet cache including size, entries, and age.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cachePath, err := getCachePath()
		if err != nil {
			return err
		}

		db, err := cache.Open(cachePath)
		if err != nil {
			return err
		}
		defer db.Close()

		stats := db.GetStats()
		term.Printf("Cache statistics:\n")
		term.Printf("  Database: %s\n", cachePath)
		term.Printf("  Size: %d bytes (%.2f KB)\n", stats.DBSize, float64(stats.DBSize)/1024)
		term.Printf("  Total entries: %d\n", stats.TotalEntries)
		if stats.TotalEntries > 0 {
			term.Printf("  Oldest entry: %s\n", stats.OldestEntry.Format(time.RFC3339))
			term.Printf("  Newest entry: %s\n", stats.NewestEntry.Format(time.RFC3339))
		}
		return nil
	},
}

func init() {
	cacheCmd.AddCommand(cacheStatsCmd)
}

// getCachePath returns the path to the cache database.
func getCachePath() (string, error) {
	cfg := GetConfig()

	// Use config cache dir if set
	if cfg != nil && cfg.CacheDir != "" {
		return filepath.Join(cfg.CacheDir, "cache.db"), nil
	}

	// Find git root
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	gitRoot, err := git.FindRootFrom(cwd)
	if err != nil {
		return "", err
	}

	cacheDir := filepath.Join(gitRoot, ".donotnet")
	os.MkdirAll(cacheDir, 0755)
	return filepath.Join(cacheDir, "cache.db"), nil
}
