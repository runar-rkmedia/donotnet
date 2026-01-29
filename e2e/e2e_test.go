package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	binaryPath     string
	mainBinaryPath string
	repoRoot       string
)

func TestMain(m *testing.M) {
	// Find repo root (parent of e2e/)
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot get working directory: %v\n", err)
		os.Exit(1)
	}
	repoRoot = filepath.Dir(wd)

	// Build current branch binary
	binaryPath = filepath.Join(wd, "donotnet-test")
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build donotnet binary: %v\n%s\n", err, out)
		os.Exit(1)
	}

	// Optionally build main branch binary for comparison tests
	if os.Getenv("DONOTNET_COMPARE") == "1" {
		tmpDir, err := os.MkdirTemp("", "donotnet-main-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create temp dir for main build: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmpDir)

		cloneCmd := exec.Command("git", "clone", "--branch", "main", "--depth", "1", repoRoot, tmpDir)
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to clone main branch: %v\n%s\n", err, out)
			fmt.Fprintln(os.Stderr, "comparison tests will be skipped")
		} else {
			mainBinaryPath = filepath.Join(wd, "donotnet-main")
			buildCmd := exec.Command("go", "build", "-o", mainBinaryPath, ".")
			buildCmd.Dir = tmpDir
			if out, err := buildCmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to build main binary: %v\n%s\n", err, out)
				fmt.Fprintln(os.Stderr, "comparison tests will be skipped")
				mainBinaryPath = ""
			}
		}
	}

	code := m.Run()

	os.Remove(binaryPath)
	if mainBinaryPath != "" {
		os.Remove(mainBinaryPath)
	}

	os.Exit(code)
}

// --- Core CLI tests (no dotnet needed) ---

func TestVersion(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "version")
	assertExit(t, r, 0)
	assertContains(t, r, "donotnet")
}

func TestListHeuristics(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "list", "heuristics")
	assertExit(t, r, 0)
	assertContains(t, r, "TestFileOnly")
}

func TestUnknownCommand(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "nonexistent")
	if r.ExitCode == 0 {
		t.Error("expected non-zero exit code for unknown command")
	}
}

func TestHelp(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "--help")
	assertExit(t, r, 0)
	assertContains(t, r, "test")
	assertContains(t, r, "build")
	assertContains(t, r, "list")
}

// --- Project scanning (needs dotnet + fixture) ---

func TestPlan(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "plan")
	assertExit(t, r, 0)
	assertContains(t, r, "Core")
}

func TestListAffected(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Modify a source file so it shows as affected
	modifyFile(t, filepath.Join(dir, "Core", "Calculator.cs"), `namespace Core;

public class Calculator
{
    public int Add(int a, int b) => a + b;
    public int Subtract(int a, int b) => a - b;
    public int Multiply(int a, int b) => a * b;
    public int Divide(int a, int b) => a / b;
}
`)

	r := runCLI(t, binaryPath, dir, "list", "affected")
	assertExit(t, r, 0)
	assertContains(t, r, "Core.Tests")
}

// --- Test execution ---

func TestBasicTest(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "Core.Tests")
}

func TestTestCaching(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// First run — populates cache
	r1 := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r1, 0)

	// Second run — should use cache
	r2 := runCLI(t, binaryPath, dir, "test")
	assertExit(t, r2, 0)

	// Cached run should be significantly faster
	if r2.Duration > r1.Duration && r1.Duration > 0 {
		t.Logf("first run: %v, second run: %v (expected second to be faster)", r1.Duration, r2.Duration)
	}
}

func TestTestForce(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// First run — populates cache
	r1 := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r1, 0)

	// Force run — should re-execute
	r2 := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r2, 0)
	assertContains(t, r2, "Core.Tests")
}

func TestTestCacheInvalidation(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// First run — populates cache
	r1 := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r1, 0)

	// Modify source file
	modifyFile(t, filepath.Join(dir, "Core", "Calculator.cs"), `namespace Core;

public class Calculator
{
    public int Add(int a, int b) => a + b;
    public int Subtract(int a, int b) => a - b;
    public int Multiply(int a, int b) => a * b;
    public int Divide(int a, int b) => a / b;
}
`)

	// Next run — should re-execute due to changed file
	r2 := runCLI(t, binaryPath, dir, "test")
	assertExit(t, r2, 0)
	assertContains(t, r2, "Core.Tests")
}

func TestDirFlag(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)
	parent := filepath.Dir(dir)

	r := runCLI(t, binaryPath, parent, "-C", dir, "test", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "Core.Tests")
}

// --- Build ---

func TestBasicBuild(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "build", "--force")
	assertExit(t, r, 0)
}

// --- List tests ---

func TestListTests(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "list", "tests", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "CalculatorTests")
}

func TestListTestsCached(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// First run
	r1 := runCLI(t, binaryPath, dir, "list", "tests", "--force")
	assertExit(t, r1, 0)

	// Second run — should be cached
	r2 := runCLI(t, binaryPath, dir, "list", "tests")
	assertExit(t, r2, 0)
	assertContains(t, r2, "CalculatorTests")
}

// --- Cache ---

func TestCacheStats(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Run test first to populate cache
	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)

	r = runCLI(t, binaryPath, dir, "cache", "stats")
	assertExit(t, r, 0)

	out := strings.ToLower(r.combined())
	if !strings.Contains(out, "entr") && !strings.Contains(out, "cache") && !strings.Contains(out, "bucket") {
		t.Errorf("cache stats output doesn't look like cache info:\n%s", r.combined())
	}
}

func TestCacheClean(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Populate cache
	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)

	// Clean cache
	r = runCLI(t, binaryPath, dir, "cache", "clean")
	assertExit(t, r, 0)
}

// --- Config ---

func TestConfig(t *testing.T) {
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "config")
	assertExit(t, r, 0)
}

// --- Completion ---

func TestCompletion(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "completion", "bash")
	assertExit(t, r, 0)
	assertContains(t, r, "bash")
}

// --- Coverage ---

func TestCoverageBuild(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "coverage", "build", "--force")
	assertExit(t, r, 0)
}

// --- Did-you-mean for misspelled flags ---

func TestFlagSuggestion(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "test", "--watc")
	if r.ExitCode == 0 {
		t.Error("expected non-zero exit code for unknown flag")
	}
	assertContains(t, r, "Did you mean")
	assertContains(t, r, "--watch")
}

func TestFlagSuggestionBuild(t *testing.T) {
	r := runCLI(t, binaryPath, t.TempDir(), "build", "--ful-build")
	if r.ExitCode == 0 {
		t.Error("expected non-zero exit code for unknown flag")
	}
	assertContains(t, r, "Did you mean")
	assertContains(t, r, "--full-build")
}

// --- Misplaced dotnet flag detection ---

func TestMisplacedFilterHint(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Pass a filter expression without "--" separator
	r := runCLI(t, binaryPath, dir, "test", "Category!=Live")
	if r.ExitCode == 0 {
		t.Error("expected non-zero exit code for misplaced filter expression")
	}
	assertContains(t, r, "--")
}

// --- Suggestions (parallel test framework) ---

func TestSuggestionsShown(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// The fixture uses xunit without Meziantou.Xunit.ParallelTestFramework,
	// so the suggestion should appear.
	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "TIP")
	assertContains(t, r, "ParallelTestFramework")
}

func TestSuggestionsSuppressed(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "test", "--force", "--no-suggestions")
	assertExit(t, r, 0)
	assertNotContains(t, r, "ParallelTestFramework")
}

// --- Print output ---

func TestPrintOutput(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "test", "--force", "--print-output")
	assertExit(t, r, 0)
	// --print-output should show the dotnet test output including test results
	assertContains(t, r, "Passed!")
}

// --- Dotnet args passthrough ---

func TestDotnetArgsAfterDash(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Pass dotnet args after "--" separator
	r := runCLI(t, binaryPath, dir, "test", "--force", "--", "--verbosity", "quiet")
	assertExit(t, r, 0)
}

// --- List coverage ---

func TestListCoverage(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "list", "coverage")
	assertExit(t, r, 0)
}

// --- VCS flags ---

func TestVcsChanged(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// With no uncommitted changes, should report nothing to do
	r := runCLI(t, binaryPath, dir, "test", "--vcs-changed")
	assertExit(t, r, 0)
	assertContains(t, r, "No uncommitted changes")
}

func TestVcsRef(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Compare against HEAD (no changes)
	r := runCLI(t, binaryPath, dir, "test", "--vcs-ref=HEAD")
	assertExit(t, r, 0)
	assertContains(t, r, "No changes")
}

// --- Global flags ---

func TestVerboseFlag(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "test", "--force", "--verbose")
	assertExit(t, r, 0)
	// Verbose output should mention "Found" projects
	assertContains(t, r, "Found")
}

func TestQuietFlag(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	r := runCLI(t, binaryPath, dir, "test", "--force", "--quiet")
	assertExit(t, r, 0)
	// Quiet mode should suppress the normal status output
	assertNotContains(t, r, "Running test on")
}

// --- Keep-going ---

func TestKeepGoing(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// With passing tests, --keep-going should behave normally
	r := runCLI(t, binaryPath, dir, "test", "--force", "--keep-going")
	assertExit(t, r, 0)
	assertContains(t, r, "succeeded")
}

// --- Failed test re-run ---

func TestFailedFlag(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Run tests first so cache exists (all passing)
	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)

	// --failed with no failures should explain and run all
	r = runCLI(t, binaryPath, dir, "test", "--failed", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "failed")
}

// --- Watch mode ---

func TestWatchStartsAndWatches(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Start watch mode, wait for it to print "Watching", then kill it.
	// Fails if the message doesn't appear within 30s.
	r := runCLIWaitFor(t, binaryPath, dir, "Watching", 30*time.Second, "test", "--force", "--watch")
	assertContains(t, r, "directories")
}

// --- Untested project detection ---

func TestUntestedProjectWarning(t *testing.T) {
	needsDotnet(t)
	dir := setupFixtureWithGit(t)

	// Add a library project with no test project referencing it
	untested := filepath.Join(dir, "Untested")
	os.MkdirAll(untested, 0o755)
	modifyFile(t, filepath.Join(untested, "Untested.csproj"), `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
</Project>
`)
	modifyFile(t, filepath.Join(untested, "Foo.cs"), `namespace Untested;
public class Foo { public int Bar() => 42; }
`)

	// Update solution to include it
	addProjectToSolution(t, dir, "Untested")

	// Commit the new project
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "add untested project")

	r := runCLI(t, binaryPath, dir, "test", "--force")
	assertExit(t, r, 0)
	assertContains(t, r, "no tests")
}
