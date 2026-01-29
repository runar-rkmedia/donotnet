package cmd

import (
	"github.com/spf13/cobra"
)

var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Coverage operations",
	Long:  `Work with code coverage data.`,
}

func init() {
	rootCmd.AddCommand(coverageCmd)

	// Subcommands are added in their respective files:
	// - coverage_parse.go
	// - coverage_build.go
}
