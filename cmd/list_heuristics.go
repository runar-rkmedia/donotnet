package cmd

import (
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
	"github.com/spf13/cobra"
)

var listHeuristicsCmd = &cobra.Command{
	Use:   "heuristics",
	Short: "List available test filter heuristics",
	Long: `List all available test filter heuristics.

Heuristics are used to guess which tests to run based on changed source files.
All heuristics are opt-in and must be explicitly enabled via --heuristics flag.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := term.Color(term.ColorCyan)
		r := term.Color(term.ColorReset)
		g := term.Color(term.ColorGreen)
		y := term.Color(term.ColorYellow)
		d := term.Color(term.ColorDim)
		red := term.Color(term.ColorRed)

		term.Printf("%sDefault heuristics%s (enabled with --heuristics=default):\n\n", c, r)
		if len(testfilter.AvailableHeuristics) == 0 {
			term.Printf("  %s(none - all heuristics are opt-in)%s\n", d, r)
		} else {
			for _, h := range testfilter.AvailableHeuristics {
				term.Printf("  %s%-20s%s %s%s%s\n", g, h.Name, r, d, h.Description, r)
			}
		}

		term.Printf("\n%sOpt-in heuristics%s (must be explicitly enabled):\n\n", c, r)
		for _, h := range testfilter.OptInHeuristics {
			term.Printf("  %s%-20s%s %s%s%s\n", y, h.Name, r, d, h.Description, r)
		}

		term.Printf("\n%sUsage:%s\n", d, r)
		term.Printf("  --heuristics=%sdefault%s                      Default heuristics only%s\n", g, r, func() string {
			if len(testfilter.AvailableHeuristics) == 0 {
				return " (none)"
			}
			return ""
		}())
		term.Printf("  --heuristics=%snone%s                         Disable all heuristics\n", red, r)
		term.Printf("  --heuristics=%sTestFileOnly%s                 Enable specific heuristic\n", y, r)
		term.Printf("  --heuristics=%sNameToNameTests%s,%sDirToNamespace%s  Multiple heuristics\n", y, r, y, r)

		return nil
	},
}

func init() {
	listCmd.AddCommand(listHeuristicsCmd)
}
