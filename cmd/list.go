package cmd

import (
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects, tests, or coverage info",
	Long:  `List various information about the project structure.`,
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Subcommands are added in their respective files:
	// - list_affected.go
	// - list_tests.go
	// - list_heuristics.go
	// - list_coverage.go
}
