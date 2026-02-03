package runner

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
	"github.com/runar-rkmedia/donotnet/project"
)

// HashArgs creates a hash of command arguments for cache keys.
func HashArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	h := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return fmt.Sprintf("%x", h[:8])
}

// isNonBuildFile returns true for files that don't affect the build.
func isNonBuildFile(name string) bool {
	lower := strings.ToLower(name)
	// Documentation and readme files
	if strings.HasPrefix(lower, "readme") || strings.HasSuffix(lower, ".md") {
		return true
	}
	// CI/CD pipeline files
	if lower == "azure-pipelines.yml" || lower == ".gitlab-ci.yml" ||
		strings.HasPrefix(lower, "jenkinsfile") || lower == "dockerfile" ||
		lower == "docker-compose.yml" || lower == "docker-compose.yaml" ||
		lower == ".dockerignore" {
		return true
	}
	// Editor and IDE config
	if lower == ".editorconfig" || lower == ".gitattributes" {
		return true
	}
	return false
}

// ComputeContentHash computes a hash of all source files in the given directories.
func ComputeContentHash(root string, dirs []string) string {
	h := sha256.New()

	// Try to load .gitignore from root
	var gitIgnore *ignore.GitIgnore
	gitignorePath := filepath.Join(root, ".gitignore")
	if gi, err := ignore.CompileIgnoreFile(gitignorePath); err == nil {
		gitIgnore = gi
	}

	// Collect all source files
	var files []string
	for _, dir := range dirs {
		absDir := dir
		if !filepath.IsAbs(absDir) {
			absDir = filepath.Join(root, absDir)
		}
		filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			name := d.Name()

			if d.IsDir() {
				if project.ShouldSkipDir(name) {
					return filepath.SkipDir
				}
				return nil
			}

			// Check gitignore
			if gitIgnore != nil {
				relPath, err := filepath.Rel(root, path)
				if err == nil && gitIgnore.MatchesPath(relPath) {
					return nil
				}
			}

			// Skip non-build files
			if isNonBuildFile(name) {
				return nil
			}

			files = append(files, path)
			return nil
		})
	}

	if len(files) == 0 {
		return ""
	}

	// Sort for deterministic ordering
	sort.Strings(files)

	for _, f := range files {
		h.Write([]byte(f))
		h.Write([]byte{0})

		content, err := os.ReadFile(f)
		if err != nil {
			h.Write([]byte{})
		} else {
			h.Write(content)
		}
		h.Write([]byte{0})
	}

	return fmt.Sprintf("%x", h.Sum(nil)[:8])
}

// restoreRelevantExts lists file extensions that can affect NuGet restore.
var restoreRelevantExts = []string{".csproj", ".props", ".targets"}

// restoreRelevantFiles lists specific filenames (case-insensitive) that affect restore
// and can appear in any directory up to the git root.
var restoreRelevantFiles = []string{
	"directory.build.props",
	"directory.build.targets",
	"directory.packages.props",
	"nuget.config",
}

// canSkipRestore checks if --no-restore can be safely used.
// Returns true if obj/project.assets.json exists and is newer than
// any restore-relevant file in the project, its dependencies, or ancestor directories.
func canSkipRestore(projectPath string, relevantDirs []string, gitRoot string) bool {
	projectDir := filepath.Dir(projectPath)
	assetsPath := filepath.Join(projectDir, "obj", "project.assets.json")

	assetsInfo, err := os.Stat(assetsPath)
	if err != nil {
		return false
	}
	assetsTime := assetsInfo.ModTime()

	// Check restore-relevant files in project and dependency directories
	for _, dir := range relevantDirs {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(gitRoot, dir)
		}
		if newerRestoreFile(dir, assetsTime) {
			return false
		}
	}

	// Walk ancestor directories from the project up to gitRoot, checking for
	// Directory.Build.props, Directory.Packages.props, nuget.config, etc.
	for dir := projectDir; ; dir = filepath.Dir(dir) {
		if anyFileNewer(dir, restoreRelevantFiles, assetsTime) {
			return false
		}
		if dir == gitRoot || dir == filepath.Dir(dir) {
			break
		}
	}

	return true
}

// newerRestoreFile returns true if dir contains any file with a restore-relevant
// extension that is newer than the given time.
func newerRestoreFile(dir string, than time.Time) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		for _, ext := range restoreRelevantExts {
			if strings.HasSuffix(name, ext) {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(than) {
					return true
				}
				break
			}
		}
	}
	return false
}

// anyFileNewer returns true if any of the named files exist in dir and are newer than the given time.
func anyFileNewer(dir string, names []string, than time.Time) bool {
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if info.ModTime().After(than) {
			return true
		}
	}
	return false
}

// canSkipBuild checks if --no-build can be safely used.
// Returns true if output DLL exists and is newer than all source files
// in the project AND all its transitive dependencies.
func canSkipBuild(projectPath string, relevantDirs []string, gitRoot string) bool {
	projectDir := filepath.Dir(projectPath)
	projectName := strings.TrimSuffix(filepath.Base(projectPath), ".csproj")

	// Find the output DLL - check common locations
	var dllInfo os.FileInfo

	// Check bin/Debug and bin/Release with various target frameworks
	binDir := filepath.Join(projectDir, "bin")
	filepath.WalkDir(binDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), projectName+".dll") {
			info, err := d.Info()
			if err == nil {
				if dllInfo == nil || info.ModTime().After(dllInfo.ModTime()) {
					dllInfo = info
				}
			}
		}
		return nil
	})

	if dllInfo == nil {
		return false
	}

	// Check if any source file in any relevant directory is newer than the DLL
	newerSourceFound := false
	for _, dir := range relevantDirs {
		if newerSourceFound {
			break
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(gitRoot, dir)
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || newerSourceFound {
				return filepath.SkipAll
			}
			if d.IsDir() {
				if project.ShouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			if isNonBuildFile(d.Name()) {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			if info.ModTime().After(dllInfo.ModTime()) {
				newerSourceFound = true
				return filepath.SkipAll
			}
			return nil
		})
	}

	return !newerSourceFound
}

// sortedKeys returns sorted keys of a map.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
