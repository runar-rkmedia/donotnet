package coverage

import (
	"context"
	"fmt"
	"strings"

	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// GroupingStats holds statistics for a single project's coverage groupings.
type GroupingStats struct {
	Name           string
	TotalTests     int
	UniqueTests    int
	MethodGroups   int
	ClassGroups    int
	FileGroups     int
	ClassReduction float64
	FileReduction  float64
}

// ListGroupings lists how tests would be grouped for each granularity level.
func ListGroupings(ctx context.Context, gitRoot string, projects []*project.Project) {
	var allStats []GroupingStats

	for _, p := range projects {
		if !p.IsTest {
			continue
		}

		absProjectPath := fmt.Sprintf("%s/%s", gitRoot, p.Path)
		projectDir := fmt.Sprintf("%s/%s", gitRoot, strings.TrimSuffix(p.Path, "/"+p.Name+".csproj"))

		term.Info("%s", p.Name)

		tests, err := listTests(ctx, gitRoot, absProjectPath)
		if err != nil {
			term.Errorf("  failed to list tests: %v", err)
			continue
		}

		if len(tests) == 0 {
			term.Warn("  no tests found")
			continue
		}

		// Deduplicate (strip parameters)
		seenBase := make(map[string]bool)
		var uniqueTests []string
		for _, t := range tests {
			baseName := t
			if idx := strings.Index(t, "("); idx > 0 {
				baseName = t[:idx]
			}
			if !seenBase[baseName] {
				seenBase[baseName] = true
				uniqueTests = append(uniqueTests, baseName)
			}
		}

		term.Printf("  Total: %d tests (%d unique)\n", len(tests), len(uniqueTests))
		term.Println()

		// Method granularity
		term.Printf("  %smethod%s: %d groups (1 test each)\n",
			term.Color(term.ColorYellow), term.Color(term.ColorReset), len(uniqueTests))

		// Class granularity
		classGroups := groupTestsByClass(uniqueTests)
		classReduction := float64(len(uniqueTests)) / float64(len(classGroups))
		term.Printf("  %sclass%s:  %d groups (%.1fx reduction)\n",
			term.Color(term.ColorGreen), term.Color(term.ColorReset), len(classGroups), classReduction)

		for _, g := range classGroups {
			if len(g.tests) > 1 {
				term.Printf("    %s%s%s: %d tests\n",
					term.Color(term.ColorDim), g.name, term.Color(term.ColorReset), len(g.tests))
			}
		}

		// File granularity
		classToFile := buildClassToFileMap(projectDir)
		fileGroups := groupTestsByFile(uniqueTests, classToFile)
		fileReduction := float64(len(uniqueTests)) / float64(len(fileGroups))
		term.Printf("  %sfile%s:   %d groups (%.1fx reduction)\n",
			term.Color(term.ColorCyan), term.Color(term.ColorReset), len(fileGroups), fileReduction)

		for _, g := range fileGroups {
			if len(g.tests) > 1 {
				term.Printf("    %s%s%s: %d tests\n",
					term.Color(term.ColorDim), g.name, term.Color(term.ColorReset), len(g.tests))
			}
		}

		term.Println()

		allStats = append(allStats, GroupingStats{
			Name:           p.Name,
			TotalTests:     len(tests),
			UniqueTests:    len(uniqueTests),
			MethodGroups:   len(uniqueTests),
			ClassGroups:    len(classGroups),
			FileGroups:     len(fileGroups),
			ClassReduction: classReduction,
			FileReduction:  fileReduction,
		})
	}

	if len(allStats) > 0 {
		printGroupingSummary(allStats)
	}
}

// printGroupingSummary prints a summary table of coverage groupings.
func printGroupingSummary(stats []GroupingStats) {
	term.Println()
	term.Info("Summary")
	term.Println()

	var totalTests, totalUnique, totalMethod, totalClass, totalFile int
	for _, s := range stats {
		totalTests += s.TotalTests
		totalUnique += s.UniqueTests
		totalMethod += s.MethodGroups
		totalClass += s.ClassGroups
		totalFile += s.FileGroups
	}

	classReduction := float64(totalMethod) / float64(totalClass)
	fileReduction := float64(totalMethod) / float64(totalFile)

	nameWidth := 10
	for _, s := range stats {
		if len(s.Name) > nameWidth {
			nameWidth = len(s.Name)
		}
	}

	if term.IsPlain() {
		term.Printf("  %-*s  %8s  %8s  %8s  %8s  %8s\n",
			nameWidth, "Project", "Tests", "Method", "Class", "File", "Best")
		term.Printf("  %s  %s  %s  %s  %s  %s\n",
			strings.Repeat("-", nameWidth),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8))
	} else {
		term.Printf("  %s%-*s  %8s  %8s  %8s  %8s  %8s%s\n",
			term.Color(term.ColorBold), nameWidth, "Project", "Tests", "Method", "Class", "File", "Best", term.Color(term.ColorReset))
	}

	for _, s := range stats {
		best := "class"
		classColor := term.ColorGreen
		fileColor := term.ColorGreen
		if s.FileGroups < s.ClassGroups {
			best = "file"
			classColor = term.ColorYellow
		} else if s.ClassGroups < s.FileGroups {
			fileColor = term.ColorYellow
		}

		if term.IsPlain() {
			term.Printf("  %-*s  %8d  %8d  %8d  %8d  %8s\n",
				nameWidth, s.Name, s.UniqueTests, s.MethodGroups, s.ClassGroups, s.FileGroups, best)
		} else {
			term.Printf("  %-*s  %8d  %s%8d%s  %s%8d%s  %s%8d%s  %s%s%s\n",
				nameWidth, s.Name, s.UniqueTests,
				term.Color(term.ColorRed), s.MethodGroups, term.Color(term.ColorReset),
				term.Color(classColor), s.ClassGroups, term.Color(term.ColorReset),
				term.Color(fileColor), s.FileGroups, term.Color(term.ColorReset),
				term.Color(term.ColorGreen), best, term.Color(term.ColorReset))
		}
	}

	// Totals
	totalClassColor := term.ColorGreen
	totalFileColor := term.ColorGreen
	if totalFile < totalClass {
		totalClassColor = term.ColorYellow
	} else if totalClass < totalFile {
		totalFileColor = term.ColorYellow
	}

	if term.IsPlain() {
		term.Printf("  %s  %s  %s  %s  %s  %s\n",
			strings.Repeat("-", nameWidth),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8),
			strings.Repeat("-", 8))
		term.Printf("  %-*s  %8d  %8d  %8d  %8d\n",
			nameWidth, "TOTAL", totalUnique, totalMethod, totalClass, totalFile)
		term.Printf("  %-*s  %8s  %8s  %7.1fx  %7.1fx\n",
			nameWidth, "Reduction", "", "1.0x",
			classReduction, fileReduction)
	} else {
		term.Printf("  %s%s%s\n", term.Color(term.ColorDim),
			strings.Repeat("─", nameWidth+2+9+9+9+9+9), term.Color(term.ColorReset))
		term.Printf("  %s%-*s%s  %8d  %s%8d%s  %s%8d%s  %s%8d%s\n",
			term.Color(term.ColorBold), nameWidth, "TOTAL", term.Color(term.ColorReset),
			totalUnique,
			term.Color(term.ColorRed), totalMethod, term.Color(term.ColorReset),
			term.Color(totalClassColor), totalClass, term.Color(term.ColorReset),
			term.Color(totalFileColor), totalFile, term.Color(term.ColorReset))
		term.Printf("  %s%-*s%s  %8s  %s%8s%s  %s%7.1fx%s  %s%7.1fx%s\n",
			term.Color(term.ColorBold), nameWidth, "Reduction", term.Color(term.ColorReset),
			"",
			term.Color(term.ColorRed), "1.0x", term.Color(term.ColorReset),
			term.Color(totalClassColor), classReduction, term.Color(term.ColorReset),
			term.Color(totalFileColor), fileReduction, term.Color(term.ColorReset))
	}

	term.Println()

	// Recommendation
	recommended := "class"
	if fileReduction > classReduction*1.2 {
		recommended = "file"
	}
	term.Printf("  %sRecommendation:%s Use %s--granularity=%s%s for best balance\n",
		term.Color(term.ColorDim), term.Color(term.ColorReset),
		term.Color(term.ColorGreen), recommended, term.Color(term.ColorReset))
	term.Println()
}
