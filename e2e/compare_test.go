package e2e

import (
	"strings"
	"testing"
)

// Comparison tests build the main branch binary and compare outputs.
// Gated behind DONOTNET_COMPARE=1 environment variable.
//
// The main branch uses flag-style CLI (e.g. -list-tests, -dev-plan, -force)
// while the current branch uses cobra subcommands (e.g. list tests, plan, --force).
// Each test invokes the binaries with their respective syntax.

// logOutputs logs stdout/stderr from both binaries for debugging.
func logOutputs(t *testing.T, label string, current, main *cliResult) {
	t.Helper()
	t.Logf("%s current (exit %d):\n  stdout: %s\n  stderr: %s",
		label, current.ExitCode, current.Stdout, current.Stderr)
	t.Logf("%s main (exit %d):\n  stdout: %s\n  stderr: %s",
		label, main.ExitCode, main.Stdout, main.Stderr)
}

func TestCompareTest(t *testing.T) {
	needsDotnet(t)
	needsCompare(t)
	dir := setupFixtureWithGit(t)

	current := runCLI(t, binaryPath, dir, "test", "--force")
	main := runCLI(t, mainBinaryPath, dir, "-force", "test")
	logOutputs(t, "test --force", current, main)

	if current.ExitCode != main.ExitCode {
		t.Errorf("exit code mismatch: current=%d main=%d", current.ExitCode, main.ExitCode)
	}

	// Both should mention the test project somewhere in their output
	assertContains(t, current, "Core.Tests")
	assertContains(t, main, "Core.Tests")
}

func TestCompareListTests(t *testing.T) {
	needsDotnet(t)
	needsCompare(t)
	dir := setupFixtureWithGit(t)

	current := runCLI(t, binaryPath, dir, "list", "tests", "--force")
	main := runCLI(t, mainBinaryPath, dir, "-force", "-list-tests")
	logOutputs(t, "list tests", current, main)

	if current.ExitCode != main.ExitCode {
		t.Errorf("exit code mismatch: current=%d main=%d", current.ExitCode, main.ExitCode)
	}

	// Both should discover the same test methods
	for _, method := range []string{"Add_ReturnsSum", "Subtract_ReturnsDifference", "Multiply_ReturnsProduct"} {
		inCurrent := strings.Contains(current.combined(), method)
		inMain := strings.Contains(main.combined(), method)
		if inCurrent != inMain {
			t.Errorf("test method %q presence differs: current=%v main=%v", method, inCurrent, inMain)
		}
	}
}

func TestComparePlan(t *testing.T) {
	needsDotnet(t)
	needsCompare(t)
	dir := setupFixtureWithGit(t)

	current := runCLI(t, binaryPath, dir, "plan")
	// In the main branch, -dev-plan is a flag used alongside a command (test/build).
	main := runCLI(t, mainBinaryPath, dir, "-dev-plan", "test")
	logOutputs(t, "plan", current, main)

	if current.ExitCode != main.ExitCode {
		t.Errorf("exit code mismatch: current=%d main=%d", current.ExitCode, main.ExitCode)
	}

	// Both should list the Core project in their plan output
	for _, proj := range []string{"Core"} {
		inCurrent := strings.Contains(current.combined(), proj)
		inMain := strings.Contains(main.combined(), proj)
		if inCurrent != inMain {
			t.Errorf("project %q presence differs: current=%v main=%v", proj, inCurrent, inMain)
		}
	}
}

func TestCompareCoverageBuild(t *testing.T) {
	needsDotnet(t)
	needsCompare(t)
	dir := setupFixtureWithGit(t)

	// Run tests with coverage on both binaries to generate data
	r := runCLI(t, binaryPath, dir, "test", "--force", "--coverage")
	assertExit(t, r, 0)

	current := runCLI(t, binaryPath, dir, "coverage", "build")
	main := runCLI(t, mainBinaryPath, dir, "-build-test-coverage")
	logOutputs(t, "coverage build", current, main)

	if current.ExitCode != main.ExitCode {
		t.Errorf("exit code mismatch: current=%d main=%d", current.ExitCode, main.ExitCode)
	}
}
