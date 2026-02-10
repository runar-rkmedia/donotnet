package coverage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// Staleness represents the staleness state of coverage data.
type Staleness int

const (
	NotFound Staleness = iota
	Stale
	Fresh
)

// StalenessMethod specifies how to check for stale coverage.
type StalenessMethod int

const (
	StalenessGit   StalenessMethod = iota // Check git for changed files since coverage generation
	StalenessMtime                        // Check file modification times
	StalenessBoth                         // Check both git and mtime
)

// ParseStalenessMethod parses a staleness check method from string.
// Valid values: "git", "mtime", "both" (defaults to "git").
func ParseStalenessMethod(s string) StalenessMethod {
	switch strings.ToLower(s) {
	case "mtime":
		return StalenessMtime
	case "both":
		return StalenessBoth
	default:
		return StalenessGit
	}
}

// Status contains detailed information about coverage staleness.
type Status struct {
	Staleness      Staleness
	ChangedFiles   []string  // Files that changed since coverage was generated
	OldestCoverage time.Time // When the oldest coverage was generated
}

// CheckStaleness checks coverage staleness using the specified method.
func CheckStaleness(gitRoot string, method StalenessMethod) Status {
	switch method {
	case StalenessMtime:
		return checkStalenessMtime(gitRoot)
	case StalenessBoth:
		gitStatus := checkStalenessGit(gitRoot)
		mtimeStatus := checkStalenessMtime(gitRoot)

		if gitStatus.Staleness == NotFound || mtimeStatus.Staleness == NotFound {
			return Status{Staleness: NotFound}
		}
		if gitStatus.Staleness == Stale || mtimeStatus.Staleness == Stale {
			filesMap := make(map[string]bool)
			for _, f := range gitStatus.ChangedFiles {
				filesMap[f] = true
			}
			for _, f := range mtimeStatus.ChangedFiles {
				filesMap[f] = true
			}
			var combined []string
			for f := range filesMap {
				combined = append(combined, f)
			}
			sort.Strings(combined)

			oldestCov := gitStatus.OldestCoverage
			if mtimeStatus.OldestCoverage.Before(oldestCov) {
				oldestCov = mtimeStatus.OldestCoverage
			}
			return Status{
				Staleness:      Stale,
				ChangedFiles:   combined,
				OldestCoverage: oldestCov,
			}
		}
		return Status{Staleness: Fresh, OldestCoverage: gitStatus.OldestCoverage}
	default:
		return checkStalenessGit(gitRoot)
	}
}

// GetSuggestion returns a coverage suggestion if coverage is missing or stale.
// Returns id, title, description (all empty if no suggestion).
func GetSuggestion(gitRoot string, method StalenessMethod) (id, title, description string) {
	status := CheckStaleness(gitRoot, method)

	switch status.Staleness {
	case NotFound:
		return "coverage-not-found",
			"Enable coverage-based test filtering",
			"Run with `donotnet coverage build` to enable coverage-based test filtering"
	case Stale:
		if len(status.ChangedFiles) <= 3 {
			return "coverage-stale",
				"Update test coverage data",
				fmt.Sprintf("Coverage may be stale (%s changed). Run `donotnet coverage build` to update",
					strings.Join(status.ChangedFiles, ", "))
		}
		return "coverage-stale",
			"Update test coverage data",
			fmt.Sprintf("Coverage may be stale (%d file(s) changed). Run `donotnet coverage build` to update",
				len(status.ChangedFiles))
	}
	return "", "", ""
}

// checkStalenessGit checks staleness by looking at git changes since coverage generation.
func checkStalenessGit(gitRoot string) Status {
	cacheDir := filepath.Join(gitRoot, ".donotnet")

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return Status{Staleness: NotFound}
	}

	var oldestGenerated time.Time
	foundAny := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".testcoverage.json") {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		covMap, loadErr := testfilter.LoadTestCoverageMap(path)
		if loadErr != nil {
			continue
		}
		foundAny = true
		if oldestGenerated.IsZero() || covMap.GeneratedAt.Before(oldestGenerated) {
			oldestGenerated = covMap.GeneratedAt
		}
	}

	if !foundAny {
		return Status{Staleness: NotFound}
	}

	changedFiles := getFilesChangedSince(gitRoot, oldestGenerated)

	var relevantChanges []string
	for _, f := range changedFiles {
		if project.IsRelevantForCoverage(f) {
			relevantChanges = append(relevantChanges, f)
		}
	}

	if len(relevantChanges) > 0 {
		return Status{
			Staleness:      Stale,
			ChangedFiles:   relevantChanges,
			OldestCoverage: oldestGenerated,
		}
	}

	return Status{Staleness: Fresh, OldestCoverage: oldestGenerated}
}

// checkStalenessMtime checks staleness by comparing file modification times.
func checkStalenessMtime(gitRoot string) Status {
	cacheDir := filepath.Join(gitRoot, ".donotnet")

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return Status{Staleness: NotFound}
	}

	var oldestGenerated time.Time
	coveredFiles := make(map[string]bool)
	foundAny := false

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".testcoverage.json") {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		covMap, loadErr := testfilter.LoadTestCoverageMap(path)
		if loadErr != nil {
			continue
		}
		foundAny = true
		if oldestGenerated.IsZero() || covMap.GeneratedAt.Before(oldestGenerated) {
			oldestGenerated = covMap.GeneratedAt
		}
		for f := range covMap.FileToTests {
			coveredFiles[f] = true
		}
	}

	if !foundAny {
		return Status{Staleness: NotFound}
	}

	var modifiedFiles []string
	for relPath := range coveredFiles {
		absPath := filepath.Join(gitRoot, relPath)
		info, statErr := os.Stat(absPath)
		if statErr != nil {
			continue
		}
		if info.ModTime().After(oldestGenerated) {
			modifiedFiles = append(modifiedFiles, relPath)
		}
	}

	if len(modifiedFiles) > 0 {
		return Status{
			Staleness:      Stale,
			ChangedFiles:   modifiedFiles,
			OldestCoverage: oldestGenerated,
		}
	}

	return Status{Staleness: Fresh, OldestCoverage: oldestGenerated}
}

// getFilesChangedSince returns files changed (committed or uncommitted) since the given time.
func getFilesChangedSince(gitRoot string, since time.Time) []string {
	filesMap := make(map[string]bool)

	// Get files from commits since the given time
	sinceStr := since.Format("2006-01-02T15:04:05")
	cmd := exec.Command("git", "-C", gitRoot, "log", "--name-only", "--pretty=format:", "--since="+sinceStr)
	out, err := cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				filesMap[line] = true
			}
		}
	}

	// Get uncommitted files, but only include them if they were modified after 'since'
	// This avoids false positives when coverage was built with uncommitted changes
	cmd = exec.Command("git", "-C", gitRoot, "status", "--porcelain")
	cmd.Dir = gitRoot
	out, err = cmd.Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if len(line) < 3 {
				continue
			}
			file := strings.TrimSpace(line[3:])
			if idx := strings.Index(file, " -> "); idx >= 0 {
				file = file[idx+4:]
			}
			if file == "" {
				continue
			}
			// Check if the file was actually modified after coverage was generated
			absPath := filepath.Join(gitRoot, file)
			info, statErr := os.Stat(absPath)
			if statErr != nil {
				continue
			}
			if info.ModTime().After(since) {
				filesMap[file] = true
			}
		}
	}

	var files []string
	for f := range filesMap {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}
