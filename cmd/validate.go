package cmd

import (
	"fmt"
	"strings"
)

// checkForMisplacedDotnetArgs checks if any positional args look like dotnet
// filter expressions that should have been placed after the -- separator.
// posArgs are cobra's non-flag args (cmd.Flags().Args()), dashDashArgs are
// the args after -- (the RunE args parameter). If -- was used, the user
// intentionally passed args through, so we skip the check.
func checkForMisplacedDotnetArgs(command string, posArgs []string, dashDashArgs []string) error {
	if len(dashDashArgs) > 0 {
		return nil
	}
	for _, arg := range posArgs {
		// Skip args that look like path targets
		if isPathArg(arg) {
			continue
		}
		if looksLikeDotnetFilterExpr(arg) {
			return fmt.Errorf("%q looks like a dotnet filter expression but was not passed after '--'.\n\nUse: donotnet %s -- --filter %q\n  or: donotnet %s --filter %q", arg, command, arg, command, arg)
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
