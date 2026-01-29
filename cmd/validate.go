package cmd

import (
	"fmt"
	"strings"
)

// checkForMisplacedDotnetArgs checks if any args look like dotnet flags
// that should have been placed after the -- separator.
// In cobra, args passed to RunE are everything after --,
// but the user might also accidentally pass dotnet-looking args as positional args.
// This detects patterns like "Category!=Live" that aren't valid donotnet args
// but look like dotnet filter expressions.
func checkForMisplacedDotnetArgs(command string, args []string) error {
	for _, arg := range args {
		if looksLikeDotnetFilterExpr(arg) {
			return fmt.Errorf("%q looks like a dotnet filter expression but was not passed after '--'.\n\nUse: donotnet %s -- --filter %q", arg, command, arg)
		}
	}
	return nil
}

// looksLikeDotnetFilterExpr returns true if the arg looks like a dotnet test filter
// expression that should be passed after -- with --filter.
func looksLikeDotnetFilterExpr(arg string) bool {
	// Category!=Live, FullyQualifiedName~Foo, etc.
	if strings.Contains(arg, "!=") || strings.Contains(arg, "~") {
		return true
	}
	return false
}
