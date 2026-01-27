package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRoot(t *testing.T) {
	// This test runs from within the git repo, so FindRoot should succeed
	root, err := FindRoot()
	if err != nil {
		t.Fatalf("FindRoot() failed: %v", err)
	}
	if root == "" {
		t.Fatal("FindRoot() returned empty string")
	}

	// Verify the returned path contains a .git directory
	gitPath := filepath.Join(root, ".git")
	if _, err := os.Stat(gitPath); os.IsNotExist(err) {
		t.Errorf("FindRoot() returned %q but .git does not exist there", root)
	}
}

func TestGetCommit(t *testing.T) {
	root, err := FindRoot()
	if err != nil {
		t.Skip("Not in a git repository")
	}

	commit := GetCommit(root)
	if commit == "" {
		t.Error("GetCommit() returned empty string")
	}
	// Commit hash should be 7 characters (short form)
	if len(commit) < 7 {
		t.Errorf("GetCommit() returned %q, expected at least 7 chars", commit)
	}
}

func TestGetDirtyFiles(t *testing.T) {
	root, err := FindRoot()
	if err != nil {
		t.Skip("Not in a git repository")
	}

	// GetDirtyFiles should not error, though it may return empty slice
	files := GetDirtyFiles(root)
	// Just verify it doesn't panic - result depends on working tree state
	_ = files
}

func TestGetChangedFiles(t *testing.T) {
	root, err := FindRoot()
	if err != nil {
		t.Skip("Not in a git repository")
	}

	// Test with HEAD (should succeed)
	files, err := GetChangedFiles(root, "HEAD")
	if err != nil {
		t.Errorf("GetChangedFiles(HEAD) failed: %v", err)
	}
	// Result may be empty if HEAD has no changes, that's fine
	_ = files

	// Test with invalid ref (should fail)
	_, err = GetChangedFiles(root, "nonexistent-ref-that-does-not-exist")
	if err == nil {
		t.Error("GetChangedFiles(invalid-ref) should have failed")
	}
}
