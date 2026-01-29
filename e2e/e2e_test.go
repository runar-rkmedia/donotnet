package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
