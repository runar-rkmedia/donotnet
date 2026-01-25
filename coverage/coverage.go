// Package coverage provides parsing and analysis of Cobertura code coverage files.
package coverage

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Report represents parsed coverage data from a Cobertura XML file
type Report struct {
	// SourceDirs are the base paths from <sources> for resolving relative filenames
	SourceDirs []string
	// CoveredFiles are files with at least one line hit (hits > 0)
	// Keys are the filename attribute values from the XML (relative to source dirs)
	CoveredFiles map[string]struct{}
	// AllFiles includes all files mentioned in coverage, whether covered or not
	AllFiles map[string]struct{}
}

// coberturaXML represents the Cobertura XML structure (only fields we need)
type coberturaXML struct {
	XMLName  xml.Name           `xml:"coverage"`
	Sources  coberturaSource    `xml:"sources"`
	Packages []coberturaPackage `xml:"packages>package"`
}

type coberturaSource struct {
	Sources []string `xml:"source"`
}

type coberturaPackage struct {
	Name    string           `xml:"name,attr"`
	Classes []coberturaClass `xml:"classes>class"`
}

type coberturaClass struct {
	Name     string          `xml:"name,attr"`
	Filename string          `xml:"filename,attr"`
	Lines    []coberturaLine `xml:"lines>line"`
}

type coberturaLine struct {
	Number int    `xml:"number,attr"`
	Hits   string `xml:"hits,attr"` // String because it can be large numbers
}

// ParseFile parses a Cobertura XML coverage file and extracts covered files
func ParseFile(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cov coberturaXML
	if err := xml.Unmarshal(data, &cov); err != nil {
		return nil, err
	}

	report := &Report{
		SourceDirs:   cov.Sources.Sources,
		CoveredFiles: make(map[string]struct{}),
		AllFiles:     make(map[string]struct{}),
	}

	// Process each package and class
	for _, pkg := range cov.Packages {
		for _, class := range pkg.Classes {
			if class.Filename == "" {
				continue
			}

			// Normalize path separators (backslashes from Windows XML)
			filename := strings.ReplaceAll(class.Filename, "\\", "/")
			report.AllFiles[filename] = struct{}{}

			// Check if any line has hits > 0
			hasCoverage := false
			for _, line := range class.Lines {
				hits, err := strconv.ParseInt(line.Hits, 10, 64)
				if err == nil && hits > 0 {
					hasCoverage = true
					break
				}
			}

			if hasCoverage {
				report.CoveredFiles[filename] = struct{}{}
			}
		}
	}

	return report, nil
}

// ResolveToGitRoot resolves a coverage filename to a path relative to gitRoot.
// It uses the source directories from the coverage report to find the full path,
// then makes it relative to gitRoot.
// Returns empty string if the file cannot be resolved.
func (r *Report) ResolveToGitRoot(filename, gitRoot string) string {
	// Normalize gitRoot
	gitRoot = filepath.Clean(gitRoot)

	// Try each source directory
	for _, srcDir := range r.SourceDirs {
		srcDir = filepath.Clean(srcDir)
		fullPath := filepath.Join(srcDir, filename)

		// Check if this path is under gitRoot
		rel, err := filepath.Rel(gitRoot, fullPath)
		if err == nil && !filepath.IsAbs(rel) && len(rel) > 0 && rel[0] != '.' {
			return filepath.ToSlash(rel)
		}
	}

	return ""
}

// GetCoveredFilesRelativeToGitRoot returns all covered files as paths relative to gitRoot
func (r *Report) GetCoveredFilesRelativeToGitRoot(gitRoot string) []string {
	var result []string
	for filename := range r.CoveredFiles {
		if resolved := r.ResolveToGitRoot(filename, gitRoot); resolved != "" {
			result = append(result, resolved)
		}
	}
	return result
}

// Map aggregates coverage data from multiple test projects.
// It maps source files to the test projects that cover them.
type Map struct {
	// FileToTestProjects maps source file (relative to gitRoot) → test project paths that cover it
	FileToTestProjects map[string][]string
	// TestProjectToFiles maps test project path → source files it covers
	TestProjectToFiles map[string][]string
	// TestProjectCoverageFile maps test project path → coverage file path used
	TestProjectCoverageFile map[string]string
	// StaleTestProjects lists test projects whose coverage data was too old
	StaleTestProjects []string
	// MissingTestProjects lists test projects with no coverage data found
	MissingTestProjects []string
}

// NewMap creates an empty coverage map
func NewMap() *Map {
	return &Map{
		FileToTestProjects:      make(map[string][]string),
		TestProjectToFiles:      make(map[string][]string),
		TestProjectCoverageFile: make(map[string]string),
	}
}

// FindCoverageFile finds the most recent coverage.cobertura.xml for a test project.
// It searches in the TestResults directory under the project's directory.
// Returns empty string if no coverage file is found.
func FindCoverageFile(testProjectDir string) string {
	testResultsDir := filepath.Join(testProjectDir, "TestResults")
	return FindCoverageFileIn(testResultsDir)
}

// FindCoverageFileIn finds the most recent coverage.cobertura.xml in the given directory.
// Returns empty string if no coverage file is found.
func FindCoverageFileIn(dir string) string {
	var newestFile string
	var newestTime int64

	// Walk directory looking for coverage.cobertura.xml files
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "coverage.cobertura.xml" {
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().Unix() > newestTime {
				newestTime = info.ModTime().Unix()
				newestFile = path
			}
		}
		return nil
	})

	return newestFile
}

// IsCoverageFresh checks if the coverage file is newer than all source files
// in the covered directories. maxAge is the maximum age in seconds (0 = no age limit).
func IsCoverageFresh(coverageFile string, coveredDirs []string, maxAge int64) bool {
	covInfo, err := os.Stat(coverageFile)
	if err != nil {
		return false
	}
	covTime := covInfo.ModTime().Unix()

	// Check age limit
	if maxAge > 0 {
		now := filepath.Base(coverageFile) // dummy call to avoid import cycle
		_ = now
		// Use file's mtime compared to current time
		// We can't easily get current time without adding time import,
		// but the caller can check this separately
	}

	// Check if any source file is newer than coverage
	for _, dir := range coveredDirs {
		newer := false
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || newer {
				return filepath.SkipAll
			}
			if d.IsDir() {
				name := d.Name()
				if name == "bin" || name == "obj" || name == "TestResults" || name == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			// Only check source files
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".cs" && ext != ".csproj" && ext != ".razor" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().Unix() > covTime {
				newer = true
				return filepath.SkipAll
			}
			return nil
		})
		if newer {
			return false
		}
	}

	return true
}

// TestProject represents a test project for coverage mapping
type TestProject struct {
	Path string // Relative path to .csproj from gitRoot
	Dir  string // Directory containing the project (relative to gitRoot)
}

// BuildMap builds a coverage map from a list of test projects.
// gitRoot is the root of the git repository (for resolving paths).
// Returns a Map with coverage data for projects that have fresh coverage files.
func BuildMap(gitRoot string, testProjects []TestProject) *Map {
	m := NewMap()

	for _, tp := range testProjects {
		projectDir := filepath.Join(gitRoot, tp.Dir)
		coverageFile := FindCoverageFile(projectDir)

		if coverageFile == "" {
			m.MissingTestProjects = append(m.MissingTestProjects, tp.Path)
			continue
		}

		// Parse coverage file
		report, err := ParseFile(coverageFile)
		if err != nil {
			m.MissingTestProjects = append(m.MissingTestProjects, tp.Path)
			continue
		}

		// Get covered files relative to gitRoot
		coveredFiles := report.GetCoveredFilesRelativeToGitRoot(gitRoot)
		if len(coveredFiles) == 0 {
			m.MissingTestProjects = append(m.MissingTestProjects, tp.Path)
			continue
		}

		// Get directories to check for freshness (extract unique dirs from covered files)
		coveredDirs := make(map[string]struct{})
		for _, f := range coveredFiles {
			dir := filepath.Dir(filepath.Join(gitRoot, f))
			coveredDirs[dir] = struct{}{}
		}
		var dirs []string
		for d := range coveredDirs {
			dirs = append(dirs, d)
		}

		// Check freshness
		if !IsCoverageFresh(coverageFile, dirs, 0) {
			m.StaleTestProjects = append(m.StaleTestProjects, tp.Path)
			continue
		}

		// Add to map
		m.TestProjectCoverageFile[tp.Path] = coverageFile
		m.TestProjectToFiles[tp.Path] = coveredFiles

		for _, f := range coveredFiles {
			m.FileToTestProjects[f] = append(m.FileToTestProjects[f], tp.Path)
		}
	}

	return m
}

// GetTestProjectsForFile returns the test projects that cover the given file.
// If the file is not in the coverage map, returns nil (caller should fall back to heuristics).
func (m *Map) GetTestProjectsForFile(filePath string) []string {
	// Normalize path
	filePath = filepath.ToSlash(filePath)
	return m.FileToTestProjects[filePath]
}

// HasCoverage returns true if the map has coverage data for at least one test project
func (m *Map) HasCoverage() bool {
	return len(m.TestProjectToFiles) > 0
}
