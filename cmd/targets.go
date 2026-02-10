package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/spf13/cobra"
)

// splitArgsAtDash splits cobra args into positional path arguments (before --)
// and dotnet passthrough arguments (after --).
//
// Cobra's ArgsLenAtDash() returns:
//   - -1 if no "--" was present (all args are positional)
//   - 0 if "--" was first (no positional args)
//   - N if there are N positional args before "--"
func splitArgsAtDash(cmd *cobra.Command, args []string) (paths []string, dotnetArgs []string) {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		// No "--" present, all args are positional
		return args, nil
	}
	// Split at the dash position
	return args[:dash], args[dash:]
}

// resolveTargets validates that each path exists and resolves them to absolute paths.
// Accepted paths: .csproj files, .sln files, or directories.
func resolveTargets(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	var resolved []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolving path %q: %w", p, err)
		}

		info, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("target path does not exist: %s", p)
			}
			return nil, fmt.Errorf("checking target path %q: %w", p, err)
		}

		if info.IsDir() {
			resolved = append(resolved, abs)
			continue
		}

		ext := strings.ToLower(filepath.Ext(abs))
		if ext != ".csproj" && ext != ".sln" {
			return nil, fmt.Errorf("target %q is not a .csproj, .sln, or directory", p)
		}
		resolved = append(resolved, abs)
	}
	return resolved, nil
}

// injectMappedFlags prepends mapped flag values into dotnetArgs.
// This allows --filter and -c/--configuration to be native donotnet flags
// while still being passed through to dotnet.
func injectMappedFlags(dotnetArgs []string, filter string, configuration string) []string {
	var prepend []string
	if filter != "" {
		prepend = append(prepend, "--filter", filter)
	}
	if configuration != "" {
		prepend = append(prepend, "-c", configuration)
	}
	if len(prepend) == 0 {
		return dotnetArgs
	}
	return append(prepend, dotnetArgs...)
}

// checkFilterConflict returns an error if --filter is specified both as a
// native donotnet flag and after --.
func checkFilterConflict(nativeFilter string, dotnetArgs []string) error {
	if nativeFilter == "" {
		return nil
	}
	passthroughFilter := runner.ExtractFilter(dotnetArgs)
	if passthroughFilter != "" {
		return fmt.Errorf("--filter specified both as a flag and after '--'.\n\nUse one or the other:\n  donotnet test --filter %q\n  donotnet test -- --filter %q", nativeFilter, passthroughFilter)
	}
	return nil
}

// isPathArg returns true if the argument looks like a path target
// (ends in .csproj, .sln, or is an existing directory).
func isPathArg(arg string) bool {
	ext := strings.ToLower(filepath.Ext(arg))
	if ext == ".csproj" || ext == ".sln" {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}
