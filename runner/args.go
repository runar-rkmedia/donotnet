package runner

import (
	"strings"

	"github.com/runar-rkmedia/donotnet/term"
)

// shouldAutoQuiet returns true if the dotnet args indicate an informational command
// where the user cares about the output, not the progress.
func shouldAutoQuiet(args []string) bool {
	infoArgs := map[string]bool{
		"--list-tests": true, // list available tests
		"--version":    true, // show version
		"--help":       true, // show help
		"-h":           true, // show help (short)
		"-?":           true, // show help (alt)
	}
	for _, arg := range args {
		if infoArgs[arg] {
			return true
		}
	}
	return false
}

// extractFilter returns the --filter value from args, or "" if none.
// Handles both "--filter" "value" and "--filter=value" forms.
func extractFilter(args []string) string {
	for i, arg := range args {
		if arg == "--filter" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--filter=") {
			return strings.TrimPrefix(arg, "--filter=")
		}
	}
	return ""
}

// removeFilter returns a copy of args with --filter (and its value) removed.
func removeFilter(args []string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--filter" && i+1 < len(args) {
			i++ // skip value
			continue
		}
		if strings.HasPrefix(args[i], "--filter=") {
			continue
		}
		result = append(result, args[i])
	}
	return result
}

// combineFilter merges an extra filter expression into existing args.
// If the args already contain --filter, the expressions are combined with &.
// Otherwise a new --filter arg is appended.
func combineFilter(args []string, extra string) []string {
	existing := extractFilter(args)
	if existing != "" {
		without := removeFilter(args)
		return append(without, "--filter", "("+existing+")&("+extra+")")
	}
	return append(append([]string{}, args...), "--filter", extra)
}

// removeCategoryFromFilter strips all Category-related clauses from a dotnet
// test filter string. This is used when an interactive trait override replaces
// the category filter so that contradictions like
// (Category!=Live)&(Category=Live) are avoided.
func removeCategoryFromFilter(filter string) string {
	// Split on & and keep only non-Category parts
	parts := strings.Split(filter, "&")
	var kept []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		// Strip wrapping parens for the check
		inner := strings.TrimPrefix(strings.TrimSuffix(trimmed, ")"), "(")
		if strings.HasPrefix(inner, "Category=") || strings.HasPrefix(inner, "Category!=") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "&")
}

// parseCategoryFilters extracts Category clauses from a dotnet filter string.
// Returns two maps: included["Live"]=true for Category=Live, excluded["Live"]=true for Category!=Live.
func parseCategoryFilters(filter string) (included, excluded map[string]bool) {
	included = make(map[string]bool)
	excluded = make(map[string]bool)
	if filter == "" {
		return
	}
	for _, p := range strings.Split(filter, "&") {
		inner := strings.TrimSpace(p)
		inner = strings.TrimPrefix(strings.TrimSuffix(inner, ")"), "(")
		if strings.HasPrefix(inner, "Category!=") {
			excluded[strings.TrimPrefix(inner, "Category!=")] = true
		} else if strings.HasPrefix(inner, "Category=") {
			included[strings.TrimPrefix(inner, "Category=")] = true
		}
	}
	return
}

// filterBuildArgs removes test-specific arguments that shouldn't be passed to dotnet build.
func filterBuildArgs(args []string) []string {
	args = removeFilter(args)
	var filtered []string
	for _, arg := range args {
		// Skip --blame flags (test-specific)
		if arg == "--blame" || arg == "--blame-hang" || arg == "--blame-crash" {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

// filterDisplayArgs filters extra args for display, removing internal/verbose flags.
func filterDisplayArgs(args []string) []string {
	var display []string
	skip := false
	for _, arg := range args {
		if skip {
			skip = false
			continue
		}
		// Skip internal flags that aren't useful for display
		if strings.HasPrefix(arg, "--logger:") || strings.HasPrefix(arg, "--results-directory:") {
			continue
		}
		// Skip property flags that are verbose
		if strings.HasPrefix(arg, "--property:") || strings.HasPrefix(arg, "-p:") {
			continue
		}
		// Skip the next arg if this is a flag that takes a value
		if arg == "--logger" || arg == "--results-directory" || arg == "-l" || arg == "-r" {
			skip = true
			continue
		}
		display = append(display, arg)
	}
	return display
}

// formatExtraArgs formats extra args for display in status messages.
// Returns empty string if no displayable args, or " (args...)" otherwise.
func formatExtraArgs(args []string) string {
	display := filterDisplayArgs(args)
	if len(display) == 0 {
		return ""
	}
	if term.IsPlain() {
		return " (" + strings.Join(display, " ") + ")"
	}
	return " " + term.ColorYellow + "(" + strings.Join(display, " ") + ")" + term.ColorReset
}
