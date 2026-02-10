package runner

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testresults"
)

// FailureInfo contains enhanced information about a test failure.
type FailureInfo struct {
	TestName   string   // Fully qualified test name
	SourceFile string   // Path to source file
	LineNumber string   // Line number in source file
	DocSummary []string // XML doc summary lines (cleaned)
}

// Regex patterns for extracting info from dotnet output
var (
	// Matches stack trace lines: "at Namespace.Class.Method() in /path/file.cs:line 123"
	stackTracePathRegex = regexp.MustCompile(`in\s+(.+\.cs):line\s+(\d+)`)

	// Matches file paths in output (for highlighting)
	filePathRegex = regexp.MustCompile(`(/[^\s:]+\.cs)(?::line\s*(\d+))?`)

	// Matches Windows-style paths too: "C:\path\file.cs:line 123"
	windowsPathRegex = regexp.MustCompile(`([A-Za-z]:\\[^\s:]+\.cs)(?::line\s*(\d+))?`)
)

// printFailureDocstrings extracts and prints docstrings for all failed tests.
// This is called after test completion to show helpful context for failures.
func printFailureDocstrings(failures []runResult, gitRoot string) {
	var allInfos []FailureInfo

	for _, f := range failures {
		failedTests := testresults.ParseStdout(f.output)
		if len(failedTests) == 0 {
			continue
		}

		infos := extractFailureInfos(f.output, failedTests)
		for _, info := range infos {
			if len(info.DocSummary) > 0 {
				allInfos = append(allInfos, info)
			}
		}
	}

	if len(allInfos) == 0 {
		return
	}

	term.Printf("\n")
	if term.IsPlain() {
		term.Printf("--- Test Documentation ---\n")
	} else {
		term.Printf("%s--- Test Documentation ---%s\n", term.ColorCyan, term.ColorReset)
	}

	for _, info := range allInfos {
		// Print test name with source location
		location := ""
		if info.SourceFile != "" {
			basename := filepath.Base(info.SourceFile)
			if info.LineNumber != "" {
				location = basename + ":" + info.LineNumber
			} else {
				location = basename
			}
		}

		if term.IsPlain() {
			if location != "" {
				term.Printf("\n  %s (%s)\n", info.TestName, location)
			} else {
				term.Printf("\n  %s\n", info.TestName)
			}
		} else {
			if location != "" {
				term.Printf("\n  %s%s%s %s(%s)%s\n", term.ColorYellow, info.TestName, term.ColorReset, term.ColorDim, location, term.ColorReset)
			} else {
				term.Printf("\n  %s%s%s\n", term.ColorYellow, info.TestName, term.ColorReset)
			}
		}

		// Print doc summary
		for _, line := range info.DocSummary {
			if term.IsPlain() {
				term.Printf("    %s\n", line)
			} else {
				term.Printf("    %s%s%s\n", term.ColorDim, line, term.ColorReset)
			}
		}
	}
	term.Printf("\n")
}

// printRerunHints prints helpful commands to rerun failed tests.
func printRerunHints(failures []runResult) {
	// Collect all failed test names
	var failedTestNames []string
	for _, f := range failures {
		failedTests := testresults.ParseStdout(f.output)
		for _, ft := range failedTests {
			failedTestNames = append(failedTestNames, ft.FullyQualifiedName)
		}
	}

	if len(failedTestNames) == 0 {
		return
	}

	if term.IsPlain() {
		term.Printf("--- Rerun Failed Tests ---\n")
	} else {
		term.Printf("%s--- Rerun Failed Tests ---%s\n", term.ColorCyan, term.ColorReset)
	}

	// Show filter command for specific test(s)
	if len(failedTestNames) == 1 {
		if term.IsPlain() {
			term.Printf("  donotnet test --filter \"FullyQualifiedName=%s\"\n", failedTestNames[0])
		} else {
			term.Printf("  %sdonotnet test --filter \"FullyQualifiedName=%s\"%s\n",
				term.ColorDim, failedTestNames[0], term.ColorReset)
		}
	} else {
		// For multiple tests, show a filter with OR
		if term.IsPlain() {
			term.Printf("  donotnet test --filter \"FullyQualifiedName=%s\"\n", failedTestNames[0])
			if len(failedTestNames) > 1 {
				term.Printf("  (or use --failed to rerun all %d failed tests)\n", len(failedTestNames))
			}
		} else {
			term.Printf("  %sdonotnet test --filter \"FullyQualifiedName=%s\"%s\n",
				term.ColorDim, failedTestNames[0], term.ColorReset)
			if len(failedTestNames) > 1 {
				term.Printf("  %s(or use --failed to rerun all %d failed tests)%s\n",
					term.ColorDim, len(failedTestNames), term.ColorReset)
			}
		}
	}

	// Always show the --failed option
	if term.IsPlain() {
		term.Printf("  donotnet test --failed\n")
	} else {
		term.Printf("  %sdonotnet test --failed%s\n", term.ColorDim, term.ColorReset)
	}
	term.Printf("\n")
}

// EnhanceFailureOutput processes test failure output to:
// 1. Extract and display docstrings for failing tests
// 2. Highlight file paths for better readability
func EnhanceFailureOutput(output string, gitRoot string) string {
	if term.IsPlain() {
		return output
	}

	// Parse failed tests from output
	failedTests := testresults.ParseStdout(output)
	if len(failedTests) == 0 {
		// No specific tests identified, just enhance paths
		return highlightPaths(output)
	}

	// Try to extract docstrings for each failed test
	infos := extractFailureInfos(output, failedTests)

	// Print docstrings for tests that have them
	var enhanced strings.Builder
	if len(infos) > 0 {
		enhanced.WriteString(term.Color(term.ColorCyan))
		enhanced.WriteString("Test Documentation:\n")
		enhanced.WriteString(term.Color(term.ColorReset))

		for _, info := range infos {
			if len(info.DocSummary) > 0 {
				// Print test name
				enhanced.WriteString(term.Color(term.ColorYellow))
				enhanced.WriteString("  ")
				enhanced.WriteString(info.TestName)
				enhanced.WriteString(term.Color(term.ColorReset))
				enhanced.WriteString("\n")

				// Print doc summary
				for _, line := range info.DocSummary {
					enhanced.WriteString(term.Color(term.ColorDim))
					enhanced.WriteString("    ")
					enhanced.WriteString(line)
					enhanced.WriteString(term.Color(term.ColorReset))
					enhanced.WriteString("\n")
				}
				enhanced.WriteString("\n")
			}
		}
	}

	// Add the original output with highlighted paths
	enhanced.WriteString(highlightPaths(output))

	return enhanced.String()
}

// extractFailureInfos extracts failure information including docstrings from source files.
func extractFailureInfos(output string, failedTests []testresults.FailedTest) []FailureInfo {
	var infos []FailureInfo

	// Build a map of test names to their source locations from stack traces
	testLocations := extractTestLocations(output, failedTests)

	for _, ft := range failedTests {
		info := FailureInfo{
			TestName: ft.FullyQualifiedName,
		}

		// Try to find source location
		if loc, ok := testLocations[ft.FullyQualifiedName]; ok {
			info.SourceFile = loc.file
			info.LineNumber = loc.line

			// Try to extract docstring from source file
			if docLines := extractDocString(loc.file, loc.line); len(docLines) > 0 {
				info.DocSummary = docLines
			}
		}

		if len(info.DocSummary) > 0 {
			infos = append(infos, info)
		}
	}

	return infos
}

type sourceLocation struct {
	file string
	line string
}

// extractTestLocations parses stack traces to find source file locations for each test.
func extractTestLocations(output string, failedTests []testresults.FailedTest) map[string]sourceLocation {
	locations := make(map[string]sourceLocation)

	// Build a set of test method names for matching
	testMethods := make(map[string]string) // methodName -> FQN
	for _, ft := range failedTests {
		// Extract method name from FQN: "Namespace.Class.TestMethod" -> "TestMethod"
		parts := strings.Split(ft.FullyQualifiedName, ".")
		if len(parts) > 0 {
			methodName := parts[len(parts)-1]
			testMethods[methodName] = ft.FullyQualifiedName
		}
	}

	// Scan output for stack trace lines
	scanner := bufio.NewScanner(strings.NewReader(output))
	var currentTest string

	for scanner.Scan() {
		line := scanner.Text()

		// Check if this line mentions a test we're tracking
		for methodName, fqn := range testMethods {
			if strings.Contains(line, methodName+"(") || strings.Contains(line, methodName+" ") {
				currentTest = fqn
				break
			}
		}

		// Look for source file paths in stack traces
		if currentTest != "" {
			if match := stackTracePathRegex.FindStringSubmatch(line); match != nil {
				if _, exists := locations[currentTest]; !exists {
					locations[currentTest] = sourceLocation{
						file: match[1],
						line: match[2],
					}
				}
			}
		}
	}

	return locations
}

// extractDocString reads a C# source file and extracts the XML documentation
// comment for the method at or near the given line number.
func extractDocString(filePath string, lineNumStr string) []string {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	// Read all lines
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) == 0 {
		return nil
	}

	// Parse the target line number
	targetLine, _ := stringToInt(lineNumStr)
	if targetLine <= 0 || targetLine > len(lines) {
		return nil
	}
	targetLine-- // Convert to 0-indexed

	// Search backwards from the target line to find the method declaration
	// The target line might be inside the method body, so we look for the
	// nearest method declaration above it
	methodLine := -1
	for i := targetLine; i >= 0; i-- {
		line := lines[i]
		// Look for method declaration patterns
		if (strings.Contains(line, "public") || strings.Contains(line, "private") || strings.Contains(line, "protected") || strings.Contains(line, "internal")) &&
			(strings.Contains(line, "void") || strings.Contains(line, "async Task") || strings.Contains(line, "async ValueTask") ||
				strings.Contains(line, "Task<") || strings.Contains(line, "Task ") || strings.Contains(line, "string ") ||
				strings.Contains(line, "int ") || strings.Contains(line, "bool ")) {
			if strings.Contains(line, "(") { // Likely a method
				methodLine = i
				break
			}
		}
	}

	if methodLine < 0 {
		// Fallback: use target line directly and search backwards for docs
		methodLine = targetLine
	}

	// Search backwards from the method line to find XML doc comments
	return extractXMLDocComments(lines, methodLine)
}

// stringToInt converts a string to int (simple helper to avoid importing strconv everywhere)
func stringToInt(s string) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// extractXMLDocComments extracts XML documentation comments that precede a method.
func extractXMLDocComments(lines []string, methodLine int) []string {
	var docLines []string

	// Search backwards from the method line
	for i := methodLine - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])

		// Skip attribute lines like [Fact], [Theory], etc.
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}

		// Check for XML doc comment
		if strings.HasPrefix(line, "///") {
			// Extract content after ///
			content := strings.TrimPrefix(line, "///")
			content = strings.TrimSpace(content)
			docLines = append([]string{content}, docLines...) // Prepend to maintain order
			continue
		}

		// If we hit a non-doc line that's not whitespace, stop
		if line != "" && !strings.HasPrefix(line, "///") {
			break
		}
	}

	// Parse and clean up the XML content
	return parseXMLDocSummary(docLines)
}

// parseXMLDocSummary extracts the text content from XML doc comment lines,
// specifically looking for <summary> content.
func parseXMLDocSummary(docLines []string) []string {
	if len(docLines) == 0 {
		return nil
	}

	// Join all lines to parse as one block
	fullDoc := strings.Join(docLines, " ")

	// Extract content between <summary> and </summary>
	summaryStart := strings.Index(fullDoc, "<summary>")
	summaryEnd := strings.Index(fullDoc, "</summary>")

	if summaryStart >= 0 && summaryEnd > summaryStart {
		summary := fullDoc[summaryStart+9 : summaryEnd]
		summary = strings.TrimSpace(summary)

		if summary == "" {
			return nil
		}

		// Clean up and split into lines
		// Remove extra whitespace
		words := strings.Fields(summary)
		if len(words) == 0 {
			return nil
		}

		// Wrap text at ~70 chars
		var result []string
		var currentLine strings.Builder
		for _, word := range words {
			if currentLine.Len()+len(word)+1 > 70 && currentLine.Len() > 0 {
				result = append(result, currentLine.String())
				currentLine.Reset()
			}
			if currentLine.Len() > 0 {
				currentLine.WriteString(" ")
			}
			currentLine.WriteString(word)
		}
		if currentLine.Len() > 0 {
			result = append(result, currentLine.String())
		}

		return result
	}

	// No <summary> tags, return raw content cleaned up
	var result []string
	for _, line := range docLines {
		// Skip XML tags
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		// Remove inline XML tags
		line = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}

	return result
}

// highlightPaths enhances dotnet output by highlighting file paths.
func highlightPaths(output string) string {
	if term.IsPlain() {
		return output
	}

	// Highlight Unix-style paths
	output = filePathRegex.ReplaceAllStringFunc(output, func(match string) string {
		submatches := filePathRegex.FindStringSubmatch(match)
		if len(submatches) >= 2 {
			path := submatches[1]
			lineNum := ""
			if len(submatches) >= 3 && submatches[2] != "" {
				lineNum = ":line " + submatches[2]
			}
			// Use cyan for path, bold for line number
			return term.ColorCyan + path + term.ColorReset +
				term.ColorYellow + lineNum + term.ColorReset
		}
		return match
	})

	// Highlight Windows-style paths
	output = windowsPathRegex.ReplaceAllStringFunc(output, func(match string) string {
		submatches := windowsPathRegex.FindStringSubmatch(match)
		if len(submatches) >= 2 {
			path := submatches[1]
			lineNum := ""
			if len(submatches) >= 3 && submatches[2] != "" {
				lineNum = ":line " + submatches[2]
			}
			return term.ColorCyan + path + term.ColorReset +
				term.ColorYellow + lineNum + term.ColorReset
		}
		return match
	})

	return output
}
