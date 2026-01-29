package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testresults"
)

// getFailedTestFilter parses cached output and/or TRX file to extract failed tests
// and build a filter string for dotnet test.
// Returns empty string if no specific tests can be identified (will rerun entire project).
func getFailedTestFilter(cachedOutput []byte, reportsDir, projectName string) string {
	var failedTests []testresults.FailedTest

	// Try TRX file first (more reliable)
	trxPath := filepath.Join(reportsDir, projectName+".trx")
	if info, err := os.Stat(trxPath); err == nil && info.Size() > 0 {
		if tests, err := testresults.ParseTRXFile(trxPath); err == nil && len(tests) > 0 {
			term.Verbose("  [%s] found %d failed tests in TRX", projectName, len(tests))
			failedTests = tests
		} else if err != nil {
			term.Verbose("  [%s] TRX parse error: %v", projectName, err)
		} else {
			term.Verbose("  [%s] TRX has no failed tests", projectName)
		}
	} else {
		term.Verbose("  [%s] no TRX file at %s", projectName, trxPath)
	}

	// Fall back to parsing stdout if TRX didn't give us results
	if len(failedTests) == 0 && len(cachedOutput) > 0 {
		failedTests = testresults.ParseStdout(string(cachedOutput))
		if len(failedTests) > 0 {
			term.Verbose("  [%s] found %d failed tests in stdout", projectName, len(failedTests))
		} else {
			term.Verbose("  [%s] no failed tests found in stdout (%d bytes)", projectName, len(cachedOutput))
			lines := strings.Split(string(cachedOutput), "\n")
			for i, line := range lines {
				if i >= 10 {
					term.Verbose("  [%s]   ... (%d more lines)", projectName, len(lines)-10)
					break
				}
				if len(line) > 100 {
					term.Verbose("  [%s]   %q...", projectName, line[:100])
				} else {
					term.Verbose("  [%s]   %q", projectName, line)
				}
			}
		}
	}

	if len(failedTests) == 0 {
		return ""
	}

	return testresults.BuildFilterString(failedTests)
}
