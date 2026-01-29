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

// filterBuildArgs removes test-specific arguments that shouldn't be passed to dotnet build.
func filterBuildArgs(args []string) []string {
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Skip --filter and its value
		if arg == "--filter" {
			i++ // skip the next arg (the filter value)
			continue
		}
		if strings.HasPrefix(arg, "--filter=") {
			continue
		}
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
