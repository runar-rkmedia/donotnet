package cmd

import (
	"encoding/json"

	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var coverageParseCmd = &cobra.Command{
	Use:   "parse <file>",
	Short: "Parse a Cobertura coverage XML file",
	Long:  `Parse a Cobertura coverage XML file and print covered files as JSON.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file := args[0]

		report, err := coverage.ParseFile(file)
		if err != nil {
			return err
		}

		// Build output structure
		output := struct {
			SourceDirs   []string `json:"source_dirs"`
			CoveredFiles []string `json:"covered_files"`
			AllFiles     []string `json:"all_files"`
		}{
			SourceDirs:   report.SourceDirs,
			CoveredFiles: make([]string, 0, len(report.CoveredFiles)),
			AllFiles:     make([]string, 0, len(report.AllFiles)),
		}

		for f := range report.CoveredFiles {
			output.CoveredFiles = append(output.CoveredFiles, f)
		}
		for f := range report.AllFiles {
			output.AllFiles = append(output.AllFiles, f)
		}

		enc := json.NewEncoder(term.Stdout())
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	},
}

func init() {
	coverageCmd.AddCommand(coverageParseCmd)
}
