package runner

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// runResult holds the result of running a dotnet command on a project.
type runResult struct {
	project         *project.Project
	success         bool
	output          string
	duration        time.Duration
	skippedBuild    bool
	skippedRestore  bool
	filteredTests   bool     // true if only specific tests were run
	testClasses     []string // test classes that were run (if filtered)
	buildOnly       bool     // true if this was a build-only job (no tests)
	viaSolution     bool     // true if this was run as part of a solution build
	skippedByFilter bool     // true if all tests were excluded by user's category filter
}

// statusUpdate is sent from workers to update the status line.
type statusUpdate struct {
	project *project.Project
	line    string
}

// statusLineWriter captures output and optionally streams it to the terminal.
type statusLineWriter struct {
	project    *project.Project
	status     chan<- statusUpdate
	buffer     *bytes.Buffer
	lineBuf    []byte
	onFailure  func() // Called once when failure detected
	directMode bool
	mu         sync.Mutex
}

var failurePatterns = []string{
	"Failed!",        // Test run failed
	"] Failed ",      // Individual test failed
	"Error Message:", // Test error details
	"Build FAILED",   // MSBuild failure
}

var failureRegex = regexp.MustCompile(`Failed:\s*[1-9]\d*[,\s]`) // "Failed: N," where N > 0

// Regex to extract test stats: "Failed: X, Passed: Y, Skipped: Z, Total: N"
var testStatsRegex = regexp.MustCompile(`Failed:\s*(\d+),\s*Passed:\s*(\d+),\s*Skipped:\s*(\d+),\s*Total:\s*(\d+)`)

func extractTestStats(output string) string {
	match := testStatsRegex.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	return formatTestStats(match[1], match[2], match[3], match[4])
}

func formatTestStats(failed, passed, skipped, total string) string {
	// Plain mode - no colors
	if term.IsPlain() {
		return fmt.Sprintf("Failed: %2s  Passed: %3s  Skipped: %2s  Total: %3s", failed, passed, skipped, total)
	}

	failedN, _ := strconv.Atoi(failed)
	passedN, _ := strconv.Atoi(passed)
	skippedN, _ := strconv.Atoi(skipped)
	totalN, _ := strconv.Atoi(total)

	// Failed: dim if 0, red otherwise
	var failedStr string
	if failedN == 0 {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", term.ColorDim, failed, term.ColorReset)
	} else {
		failedStr = fmt.Sprintf("%sFailed: %2s%s", term.ColorRed, failed, term.ColorReset)
	}

	// Passed: green if passed+skipped=total (all accounted for)
	var passedStr string
	if passedN+skippedN == totalN {
		passedStr = fmt.Sprintf("%sPassed: %3s%s", term.ColorGreen, passed, term.ColorReset)
	} else {
		passedStr = fmt.Sprintf("Passed: %3s", passed)
	}

	// Skipped: dim if 0, yellow otherwise
	var skippedStr string
	if skippedN == 0 {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", term.ColorDim, skipped, term.ColorReset)
	} else {
		skippedStr = fmt.Sprintf("%sSkipped: %2s%s", term.ColorYellow, skipped, term.ColorReset)
	}

	totalStr := fmt.Sprintf("Total: %3s", total)

	return fmt.Sprintf("%s  %s  %s  %s", failedStr, passedStr, skippedStr, totalStr)
}

func (w *statusLineWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	w.buffer.Write(p)

	w.mu.Lock()
	directMode := w.directMode
	w.mu.Unlock()

	// In direct mode, just print to stderr
	if directMode {
		term.Write(p)
		return n, nil
	}

	// Process line by line
	w.lineBuf = append(w.lineBuf, p...)
	for {
		idx := bytes.IndexByte(w.lineBuf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(w.lineBuf[:idx]))
		w.lineBuf = w.lineBuf[idx+1:]

		if line != "" {
			select {
			case w.status <- statusUpdate{project: w.project, line: line}:
			default: // Don't block
			}

			// Check for failure patterns
			isFailure := false
			for _, pattern := range failurePatterns {
				if strings.Contains(line, pattern) {
					isFailure = true
					break
				}
			}
			if !isFailure && failureRegex.MatchString(line) {
				isFailure = true
			}

			if isFailure {
				w.mu.Lock()
				if !w.directMode {
					w.directMode = true
					// Clear status line and print header
					if term.IsPlain() {
						term.Status("  FAIL %s\n\n", w.project.Name)
					} else {
						term.Status("  %s✗%s %s\n\n", term.ColorRed, term.ColorReset, w.project.Name)
					}
					// Print buffered output
					term.Write(w.buffer.Bytes())
					if w.onFailure != nil {
						w.onFailure()
					}
				}
				w.mu.Unlock()
			}
		}
	}
	return n, nil
}

// prettyPrintFilter prints a test filter in a readable format.
func prettyPrintFilter(projectName, filter string) {
	// Parse filter: "FullyQualifiedName~Foo|FullyQualifiedName~Bar" -> ["Foo", "Bar"]
	parts := strings.Split(filter, "|")
	if len(parts) == 0 {
		return
	}

	var testNames []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "FullyQualifiedName~") {
			testNames = append(testNames, strings.TrimPrefix(part, "FullyQualifiedName~"))
		} else if strings.HasPrefix(part, "FullyQualifiedName=") {
			testNames = append(testNames, strings.TrimPrefix(part, "FullyQualifiedName="))
		} else if part != "" {
			testNames = append(testNames, part)
		}
	}

	if len(testNames) == 0 {
		return
	}

	// Print project name and test count
	term.Printf("  %s (%d tests):\n", projectName, len(testNames))
	for _, name := range testNames {
		term.Dim("    %s", name)
	}
}

// needsRestoreRetry checks if dotnet output indicates a restore is needed.
func needsRestoreRetry(output string) bool {
	// Common patterns indicating restore is needed
	restorePatterns := []string{
		"Assets file .* doesn't have a target",
		"run a NuGet package restore",
		"Please restore this project",
		"project.assets.json' not found",
		"NETSDK1004:",         // Missing assets file
		"NETSDK1064:",         // Package not found / deleted since restore
		"NU1101:",             // Unable to find package
		"The project file could not be loaded",
	}

	for _, pattern := range restorePatterns {
		matched, _ := regexp.MatchString(pattern, output)
		if matched {
			return true
		}
	}
	return false
}
