package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// isNonBuildFile returns true for files that don't affect the build
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

// computeContentHash computes a hash of all source files in the given directories
func computeContentHash(root string, dirs []string) string {
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
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			name := d.Name()

			// Always skip these directories
			if d.IsDir() {
				if name == "bin" || name == "obj" || name == ".git" || name == "node_modules" {
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

			// Only include source files that affect compilation
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".cs" || ext == ".csproj" || ext == ".razor" || ext == ".props" || ext == ".targets" {
				files = append(files, path)
			}
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

// canSkipRestore checks if --no-restore can be safely used
// Returns true if obj/project.assets.json exists and is newer than
// any .csproj file in the project or its transitive dependencies
func canSkipRestore(projectPath string, relevantDirs []string) bool {
	projectDir := filepath.Dir(projectPath)
	assetsPath := filepath.Join(projectDir, "obj", "project.assets.json")

	assetsInfo, err := os.Stat(assetsPath)
	if err != nil {
		return false
	}

	// Check if any .csproj in the project or its dependencies is newer than assets.json
	for _, dir := range relevantDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".csproj") {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(assetsInfo.ModTime()) {
					return false
				}
			}
		}
	}

	return true
}

// canSkipBuild checks if --no-build can be safely used
// Returns true if output DLL exists and is newer than all source files
// in the project AND all its transitive dependencies
func canSkipBuild(projectPath string, relevantDirs []string) bool {
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
	// This includes the project's own directory AND all transitive dependencies
	newerSourceFound := false
	for _, dir := range relevantDirs {
		if newerSourceFound {
			break
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || newerSourceFound {
				return filepath.SkipAll
			}
			if d.IsDir() {
				name := d.Name()
				if name == "bin" || name == "obj" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".cs" && ext != ".csproj" && ext != ".razor" && ext != ".props" && ext != ".targets" {
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
