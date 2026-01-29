package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	rootCmd.SetFlagErrorFunc(flagErrorWithSuggestion)
}

// flagErrorWithSuggestion enhances "unknown flag" errors with did-you-mean suggestions.
func flagErrorWithSuggestion(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()
	// Look for "unknown flag: --name" or "unknown shorthand flag: 'x'"
	var unknownFlag string
	if strings.HasPrefix(msg, "unknown flag: ") {
		unknownFlag = strings.TrimPrefix(msg, "unknown flag: ")
		unknownFlag = strings.TrimLeft(unknownFlag, "-")
	} else if strings.HasPrefix(msg, "unknown shorthand flag: ") {
		unknownFlag = strings.Trim(strings.TrimPrefix(msg, "unknown shorthand flag: "), "' ")
	}

	if unknownFlag == "" {
		return err
	}

	// Collect all flags from this command and its parents
	var bestName string
	bestDist := -1

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		d := levenshtein(unknownFlag, f.Name)
		threshold := max(2, len(f.Name)*4/10)
		if d <= threshold && (bestDist < 0 || d < bestDist) {
			bestDist = d
			bestName = f.Name
		}
	})
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		d := levenshtein(unknownFlag, f.Name)
		threshold := max(2, len(f.Name)*4/10)
		if d <= threshold && (bestDist < 0 || d < bestDist) {
			bestDist = d
			bestName = f.Name
		}
	})

	if bestName != "" {
		return fmt.Errorf("%w\n\nDid you mean: --%s?", err, bestName)
	}
	return err
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				curr[j-1]+1,
				min(prev[j]+1, prev[j-1]+cost),
			)
		}
		prev = curr
	}
	return prev[lb]
}
