package cmd

import (
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	cacheCleanOlderThan int
)

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean old cache entries",
	Long: `Remove cache entries older than the specified number of days.

By default, removes entries older than 30 days.`,
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

		maxAge := time.Duration(cacheCleanOlderThan) * 24 * time.Hour
		deleted, err := db.DeleteOldEntries(maxAge)
		if err != nil {
			return err
		}

		term.Printf("Deleted %d entries older than %d days\n", deleted, cacheCleanOlderThan)
		return nil
	},
}

func init() {
	cacheCleanCmd.Flags().IntVar(&cacheCleanOlderThan, "older-than", 30, "Remove entries older than N days")
	cacheCmd.AddCommand(cacheCleanCmd)
}
