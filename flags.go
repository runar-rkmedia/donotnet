package main

import (
	"flag"
	"os"
	"strings"
)

// levenshtein computes the Levenshtein distance between two strings
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

// getDefinedFlags returns all defined flag names
func getDefinedFlags() []string {
	var names []string
	flag.VisitAll(func(f *flag.Flag) {
		names = append(names, f.Name)
	})
	return names
}

// suggestFlag finds the closest matching flag name, if any is close enough
// Returns the suggestion and whether a good match was found
func suggestFlag(unknown string, defined []string) (string, bool) {
	if len(unknown) == 0 {
		return "", false
	}

	bestMatch := ""
	bestDist := len(unknown) // max reasonable distance

	for _, name := range defined {
		dist := levenshtein(unknown, name)
		// Only suggest if distance is reasonable (at most 40% of the longer string)
		maxLen := max(len(unknown), len(name))
		threshold := max(2, maxLen*2/5)
		if dist < bestDist && dist <= threshold {
			bestDist = dist
			bestMatch = name
		}
	}

	return bestMatch, bestMatch != ""
}

// parseUnknownFlag extracts the flag name from an argument like "-flag" or "--flag" or "-flag=value"
// Returns the flag name and whether it looks like a flag
func parseUnknownFlag(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return "", false
	}

	// Strip leading dashes
	name := strings.TrimLeft(arg, "-")
	if name == "" {
		return "", false
	}

	// Handle -flag=value syntax
	if idx := strings.Index(name, "="); idx >= 0 {
		name = name[:idx]
	}

	return name, true
}

// checkForUnknownFlags pre-scans os.Args for unknown flags and prints suggestions
// Returns true if an unknown flag was found (caller should exit)
func checkForUnknownFlags() bool {
	defined := getDefinedFlags()
	definedSet := make(map[string]bool)
	for _, name := range defined {
		definedSet[name] = true
	}

	args := os.Args[1:]
	foundUnknown := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Stop at "--" separator
		if arg == "--" {
			break
		}

		// Skip non-flag arguments (commands like "test", "build")
		name, isFlag := parseUnknownFlag(arg)
		if !isFlag {
			continue
		}

		// Check if this flag is defined
		if definedSet[name] {
			// Check if this flag takes a value and skip it
			flag.VisitAll(func(f *flag.Flag) {
				if f.Name == name {
					// If the flag doesn't use = syntax and takes a value, skip the next arg
					if !strings.Contains(arg, "=") {
						// Bool flags don't consume the next arg
						if _, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok {
							// Non-bool flag, might consume next arg
							if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
								i++
							}
						}
					}
				}
			})
			continue
		}

		// Unknown flag found
		foundUnknown = true
		term.Errorf("unknown flag: -%s", name)

		if suggestion, found := suggestFlag(name, defined); found {
			g, r, d := term.Color(colorGreen), term.Color(colorReset), term.Color(colorDim)
			term.Printf("Did you mean: %s-%s%s?\n", g, suggestion, r)
			if f := flag.Lookup(suggestion); f != nil && f.Usage != "" {
				term.Printf("  %s%s%s\n", d, f.Usage, r)
			}
		}
	}

	if foundUnknown {
		term.Printf("\nRun 'donotnet -help' for usage.\n")
	}

	return foundUnknown
}
