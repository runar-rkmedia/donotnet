package cmd

import (
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Cache operations",
	Long:  `Manage the donotnet build/test cache.`,
}

func init() {
	rootCmd.AddCommand(cacheCmd)

	// Subcommands are added in their respective files:
	// - cache_stats.go
	// - cache_clean.go
	// - cache_dump.go
}
