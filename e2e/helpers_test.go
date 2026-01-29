package e2e

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// cliResult holds the output from running the CLI binary.
type cliResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// runCLI executes the binary with args in workDir and returns the result.
func runCLI(t *testing.T, binary, workDir string, args ...string) *cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run %s %v: %v", binary, args, err)
		}
	}

	return &cliResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}
}

// combined returns stdout + stderr concatenated.
func (r *cliResult) combined() string {
	return r.Stdout + r.Stderr
}

// assertExit asserts the exit code matches expected.
func assertExit(t *testing.T, r *cliResult, code int) {
	t.Helper()
	if r.ExitCode != code {
		t.Errorf("expected exit code %d, got %d\nstdout: %s\nstderr: %s", code, r.ExitCode, r.Stdout, r.Stderr)
	}
}

// assertContains checks that the combined output contains substr.
func assertContains(t *testing.T, r *cliResult, substr string) {
	t.Helper()
	if !strings.Contains(r.combined(), substr) {
		t.Errorf("output does not contain %q\nstdout: %s\nstderr: %s", substr, r.Stdout, r.Stderr)
	}
}

// assertNotContains checks that the combined output does NOT contain substr.
func assertNotContains(t *testing.T, r *cliResult, substr string) {
	t.Helper()
	if strings.Contains(r.combined(), substr) {
		t.Errorf("output unexpectedly contains %q\nstdout: %s\nstderr: %s", substr, r.Stdout, r.Stderr)
	}
}

// copyFixture copies testdata/fixture to a temp directory and returns the path.
func copyFixture(t *testing.T) string {
	t.Helper()
	src := filepath.Join("testdata", "fixture")
	dst := t.TempDir()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying fixture: %v", err)
	}
	return dst
}

// initGitRepo initializes a git repo in dir with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "add", "."},
		{"git", "commit", "-m", "initial"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git command %v failed: %v\n%s", args, err, out)
		}
	}
}

// modifyFile writes content to the given path.
func modifyFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("modifying file %s: %v", path, err)
	}
}

// needsDotnet skips the test if dotnet is not available.
func needsDotnet(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not available, skipping")
	}
}

// needsCompare skips the test if the main binary was not built.
func needsCompare(t *testing.T) {
	t.Helper()
	if mainBinaryPath == "" {
		t.Skip("main branch binary not built (set DONOTNET_COMPARE=1 to enable)")
	}
}

// runCLIWaitFor starts the binary, waits until the combined output contains waitFor,
// then kills the process and returns the captured output.
// If the text doesn't appear within timeout, the test fails.
func runCLIWaitFor(t *testing.T, binary, workDir string, waitFor string, timeout time.Duration, args ...string) *cliResult {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

	var stdout, stderr syncBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start %s: %v", binary, err)
	}

	// Poll for the expected text
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	found := false
	for !found {
		select {
		case <-deadline:
			cancel()
			_ = cmd.Wait()
			t.Fatalf("timed out waiting for %q in output.\nstdout: %s\nstderr: %s",
				waitFor, stdout.String(), stderr.String())
		case <-ticker.C:
			if strings.Contains(stdout.String()+stderr.String(), waitFor) {
				found = true
			}
		}
	}

	cancel()
	_ = cmd.Wait()
	duration := time.Since(start)

	return &cliResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: duration,
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer for capturing concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// setupFixtureWithGit copies the fixture and initializes a git repo.
func setupFixtureWithGit(t *testing.T) string {
	t.Helper()
	dir := copyFixture(t)
	initGitRepo(t, dir)
	return dir
}

// gitAdd stages files in a git repo.
func gitAdd(t *testing.T, dir string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, out)
	}
}

// gitCommit creates a commit in a git repo.
func gitCommit(t *testing.T, dir, message string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
}

// addProjectToSolution adds a project to the .sln file using dotnet sln.
func addProjectToSolution(t *testing.T, dir, projectName string) {
	t.Helper()
	cmd := exec.Command("dotnet", "sln", "add", projectName)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dotnet sln add failed: %v\n%s", err, out)
	}
}
