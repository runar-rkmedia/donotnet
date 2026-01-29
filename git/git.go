// Package git provides helper functions for git operations.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindRoot finds the root of the git repository by walking up from the current directory.
func FindRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return FindRootFrom(dir)
}

// FindRootFrom finds the root of the git repository by walking up from the given directory.
func FindRootFrom(dir string) (string, error) {
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not in a git repository")
		}
		dir = parent
	}
}

// GetCommit returns the current HEAD commit hash (short).
func GetCommit(gitRoot string) string {
	cmd := exec.Command("git", "-C", gitRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GetDirtyFiles returns a list of dirty (uncommitted) files relative to git root.
func GetDirtyFiles(gitRoot string) []string {
	cmd := exec.Command("git", "-C", gitRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 3 {
			continue
		}
		// Format: XY filename or XY orig -> renamed
		file := strings.TrimSpace(line[3:])
		// Handle renames: "old -> new"
		if idx := strings.Index(file, " -> "); idx >= 0 {
			file = file[idx+4:]
		}
		if file != "" {
			files = append(files, file)
		}
	}
	return files
}

// GetChangedFiles returns files changed compared to a ref (e.g., "main", "HEAD~3").
// Returns an error if the ref is invalid.
func GetChangedFiles(gitRoot, ref string) ([]string, error) {
	cmd := exec.Command("git", "-C", gitRoot, "diff", "--name-only", ref)
	out, err := cmd.Output()
	if err != nil {
		// Check if ref exists
		checkCmd := exec.Command("git", "-C", gitRoot, "rev-parse", "--verify", ref)
		if checkErr := checkCmd.Run(); checkErr != nil {
			return nil, fmt.Errorf("unknown git ref: %s", ref)
		}
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		file := strings.TrimSpace(line)
		if file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}
