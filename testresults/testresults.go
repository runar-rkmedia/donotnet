// Package testresults provides parsers for extracting failed test names from
// dotnet test output (TRX files and stdout).
package testresults

import (
	"bufio"
	"encoding/xml"
	"os"
	"regexp"
	"strings"
)

// FailedTest represents a test that failed
type FailedTest struct {
	FullyQualifiedName string // e.g., "MyNamespace.MyClass.TestMethod"
	DisplayName        string // e.g., "TestMethod" or "TestMethod(param: value)"
}

// TRX XML structures (Microsoft Visual Studio Test Results format)
// Namespace: http://microsoft.com/schemas/VisualStudio/TeamTest/2010

type trxTestRun struct {
	XMLName xml.Name         `xml:"TestRun"`
	Results trxResults       `xml:"Results"`
	TestDef trxTestDefs      `xml:"TestDefinitions"`
}

type trxResults struct {
	UnitTestResults []trxUnitTestResult `xml:"UnitTestResult"`
}

type trxUnitTestResult struct {
	TestName string `xml:"testName,attr"`
	Outcome  string `xml:"outcome,attr"`
	TestId   string `xml:"testId,attr"`
}

type trxTestDefs struct {
	UnitTests []trxUnitTest `xml:"UnitTest"`
}

type trxUnitTest struct {
	Id         string        `xml:"id,attr"`
	Name       string        `xml:"name,attr"`
	TestMethod trxTestMethod `xml:"TestMethod"`
}

type trxTestMethod struct {
	ClassName string `xml:"className,attr"`
	Name      string `xml:"name,attr"`
}

// ParseTRXFile parses a TRX file and returns the list of failed tests.
func ParseTRXFile(path string) ([]FailedTest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTRX(data)
}

// ParseTRX parses TRX XML content and returns the list of failed tests.
func ParseTRX(data []byte) ([]FailedTest, error) {
	var testRun trxTestRun
	if err := xml.Unmarshal(data, &testRun); err != nil {
		return nil, err
	}

	// Build map of testId -> UnitTest definition for FQN lookup
	testDefs := make(map[string]trxUnitTest)
	for _, ut := range testRun.TestDef.UnitTests {
		testDefs[ut.Id] = ut
	}

	var failed []FailedTest
	for _, result := range testRun.Results.UnitTestResults {
		if result.Outcome == "Failed" {
			ft := FailedTest{
				DisplayName: result.TestName,
			}

			// Try to get fully qualified name from test definition
			if def, ok := testDefs[result.TestId]; ok {
				className := def.TestMethod.ClassName
				// ClassName might have assembly suffix: "Namespace.ClassName, AssemblyName"
				if idx := strings.Index(className, ","); idx > 0 {
					className = strings.TrimSpace(className[:idx])
				}
				ft.FullyQualifiedName = className + "." + def.TestMethod.Name
			} else {
				// Fall back to testName which contains the FQN with parameters
				// e.g., "eDF.Common.Tests.Helpers.UtilsTests.MatchesEMail_ValidCandidate(candidate: \"test@domain.com\")"
				ft.FullyQualifiedName = extractFQNFromTestName(result.TestName)
			}

			failed = append(failed, ft)
		}
	}

	return failed, nil
}

// extractFQNFromTestName tries to extract a fully qualified name from the test name.
// Test names can be like "TestMethod" or "Namespace.Class.TestMethod" or "TestMethod(arg: value)"
func extractFQNFromTestName(testName string) string {
	// Remove parameters if present: "TestMethod(arg: value)" -> "TestMethod"
	if idx := strings.Index(testName, "("); idx > 0 {
		testName = testName[:idx]
	}
	return strings.TrimSpace(testName)
}

// extractMethodName extracts just the method name from a fully qualified test name
// e.g., "MyApp.Tests.SampleTests.TestMethod" -> "TestMethod"
func extractMethodName(fqn string) string {
	if idx := strings.LastIndex(fqn, "."); idx >= 0 {
		return fqn[idx+1:]
	}
	return fqn
}

// Regex patterns for parsing dotnet test stdout
var (
	// Matches: "  Failed Namespace.Class.TestMethod [123ms]"
	// Or: "  Failed Namespace.Class.TestMethod(arg: value) [123ms]"
	stdoutFailedRegex = regexp.MustCompile(`^\s*Failed\s+(\S+)`)

	// Matches: "  X Namespace.Class.TestMethod [123ms]" (some test adapters use X for failed)
	stdoutXFailedRegex = regexp.MustCompile(`^\s*[Xx]\s+(\S+)`)

	// Matches xUnit format: "[xUnit.net 00:00:00.13]     Namespace.Class.TestMethod [FAIL]"
	// Or with parameters: "[xUnit.net 00:00:00.13]     Namespace.Class.TestMethod(arg: value) [FAIL]"
	xunitFailedRegex = regexp.MustCompile(`\[xUnit\.net[^\]]*\]\s+(.+?)\s+\[FAIL\]`)
)

// ParseStdout parses dotnet test stdout and returns the list of failed tests.
func ParseStdout(output string) []FailedTest {
	var failed []FailedTest
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		var testName string
		if match := stdoutFailedRegex.FindStringSubmatch(line); match != nil {
			testName = match[1]
		} else if match := stdoutXFailedRegex.FindStringSubmatch(line); match != nil {
			testName = match[1]
		} else if match := xunitFailedRegex.FindStringSubmatch(line); match != nil {
			testName = match[1]
		}

		if testName != "" {
			// Remove trailing brackets like [123ms] (but not [FAIL] which is already handled)
			if idx := strings.LastIndex(testName, "["); idx > 0 {
				testName = strings.TrimSpace(testName[:idx])
			}

			// Avoid duplicates
			fqn := extractFQNFromTestName(testName)
			if !seen[fqn] {
				seen[fqn] = true
				failed = append(failed, FailedTest{
					FullyQualifiedName: fqn,
					DisplayName:        testName,
				})
			}
		}
	}

	return failed
}

// BuildFilterString builds a dotnet test --filter string from failed tests.
// Uses FullyQualifiedName~ for substring matching to handle parameterized tests.
func BuildFilterString(tests []FailedTest) string {
	if len(tests) == 0 {
		return ""
	}

	var parts []string
	for _, t := range tests {
		// Use FullyQualifiedName~ for prefix matching
		// This handles parameterized tests where the FQN includes parameters
		parts = append(parts, "FullyQualifiedName~"+t.FullyQualifiedName)
	}

	return strings.Join(parts, "|")
}
