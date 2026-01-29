package cmd

import (
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

// Heuristic represents a test filter heuristic.
type heuristicInfo struct {
	Name        string
	Description string
}

// optInHeuristics are the heuristics that must be explicitly enabled.
// These match the definitions in testfilter.go.
var optInHeuristics = []heuristicInfo{
	{"TestFileOnly", "FooTests.cs -> FooTests (filters to changed test file if it has test methods)"},
	{"NameToNameTests", "Foo.cs -> FooTests (direct name match, can miss or run wrong tests)"},
	{"DirToNamespace", "Cache/Foo.cs -> .Cache.Foo (matches tests with directory as namespace)"},
	{"ExtensionsToBase", "FooExtensions.cs -> FooTests (extension methods tested with base class)"},
	{"InterfaceToImpl", "IFoo.cs -> FooTests (interface to implementation tests)"},
	{"AlwaysCompositionRoot", "Any .cs -> CompositionRootTests (DI container tests)"},
}

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
		term.Printf("  %s(none - all heuristics are opt-in)%s\n", d, r)

		term.Printf("\n%sOpt-in heuristics%s (must be explicitly enabled):\n\n", c, r)
		for _, h := range optInHeuristics {
			term.Printf("  %s%-20s%s %s%s%s\n", y, h.Name, r, d, h.Description, r)
		}

		term.Printf("\n%sUsage:%s\n", d, r)
		term.Printf("  --heuristics=%sdefault%s                      Default heuristics only (none)\n", g, r)
		term.Printf("  --heuristics=%snone%s                         Disable all heuristics\n", red, r)
		term.Printf("  --heuristics=%sTestFileOnly%s                 Enable specific heuristic\n", y, r)
		term.Printf("  --heuristics=%sNameToNameTests%s,%sDirToNamespace%s  Multiple heuristics\n", y, r, y, r)

		return nil
	},
}

func init() {
	listCmd.AddCommand(listHeuristicsCmd)
}
