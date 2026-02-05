// Package project provides functionality for discovering and parsing .NET projects and solutions.
package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Project represents a parsed .csproj file.
type Project struct {
	Path              string   // relative path from git root
	Dir               string   // directory containing the project
	Name              string   // project name (without .csproj extension)
	References        []string // absolute paths to referenced projects
	PackageReferences []string // NuGet package names
	IsTest            bool     // true if this is a test project
}

// Solution represents a parsed .sln file.
type Solution struct {
	Path     string          // absolute path to .sln
	RelPath  string          // relative path from git root
	Projects map[string]bool // set of absolute project paths in this solution
}

var projectRefRegex = regexp.MustCompile(`<ProjectReference\s+Include="([^"]+)"`)
var packageRefRegex = regexp.MustCompile(`<PackageReference\s+Include="([^"]+)"`)
var slnProjectRegex = regexp.MustCompile(`Project\("[^"]+"\)\s*=\s*"[^"]+",\s*"([^"]+\.csproj)"`)

// Discover walks scanRoot once to find all .csproj and .sln files.
func Discover(scanRoot, gitRoot string) ([]*Project, []*Solution, error) {
	var projects []*Project
	var solutions []*Solution

	err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "bin" || name == "obj" || name == ".vs" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".csproj") {
			relPath, _ := filepath.Rel(gitRoot, path)
			p, err := Parse(path, relPath)
			if err != nil {
				return nil
			}
			projects = append(projects, p)
		} else if strings.HasSuffix(path, ".sln") {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			relPath, _ := filepath.Rel(gitRoot, path)
			slnDir := filepath.Dir(path)
			slnProjects := make(map[string]bool)

			matches := slnProjectRegex.FindAllStringSubmatch(string(content), -1)
			for _, m := range matches {
				projPath := m[1]
				projPath = strings.ReplaceAll(projPath, "\\", "/")
				absPath := filepath.Clean(filepath.Join(slnDir, projPath))
				slnProjects[absPath] = true
			}

			if len(slnProjects) > 0 {
				solutions = append(solutions, &Solution{
					Path:     path,
					RelPath:  relPath,
					Projects: slnProjects,
				})
			}
		}
		return nil
	})

	return projects, solutions, err
}

// Parse parses a .csproj file and returns a Project struct.
func Parse(path, relPath string) (*Project, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	name := strings.TrimSuffix(filepath.Base(path), ".csproj")

	// Check if it's a test project
	isTest := strings.HasSuffix(name, ".Tests") ||
		strings.HasSuffix(name, ".Test") ||
		strings.HasSuffix(name, "Tests") ||
		strings.Contains(string(content), "<IsTestProject>true</IsTestProject>")

	// Find project references
	matches := projectRefRegex.FindAllStringSubmatch(string(content), -1)
	var refs []string
	for _, m := range matches {
		refPath := m[1]
		// Convert Windows path separators
		refPath = strings.ReplaceAll(refPath, "\\", "/")
		// Resolve relative path
		absRef := filepath.Clean(filepath.Join(dir, refPath))
		refs = append(refs, absRef)
	}

	// Find package references (NuGet packages)
	pkgMatches := packageRefRegex.FindAllStringSubmatch(string(content), -1)
	var pkgRefs []string
	for _, m := range pkgMatches {
		pkgRefs = append(pkgRefs, m[1])
	}

	return &Project{
		Path:              relPath,
		Dir:               filepath.Dir(relPath),
		Name:              name,
		References:        refs,
		PackageReferences: pkgRefs,
		IsTest:            isTest,
	}, nil
}

// buildAbsToRel maps absolute project paths to their relative paths.
// Uses gitRoot to resolve relative project paths, since they are relative to the git root,
// not the current working directory.
func buildAbsToRel(projects []*Project, gitRoot string) map[string]string {
	absToRel := make(map[string]string)
	for _, p := range projects {
		abs := filepath.Join(gitRoot, p.Path)
		absToRel[abs] = p.Path
	}
	return absToRel
}

// BuildDependencyGraph returns a map of project path -> projects that depend on it (reverse graph).
func BuildDependencyGraph(projects []*Project, gitRoot string) map[string][]string {
	absToRel := buildAbsToRel(projects, gitRoot)

	// Build reverse dependency graph
	graph := make(map[string][]string)
	for _, p := range projects {
		for _, ref := range p.References {
			if relRef, ok := absToRel[ref]; ok {
				graph[relRef] = append(graph[relRef], p.Path)
			}
		}
	}
	return graph
}

// BuildForwardDependencyGraph returns a map of project path -> projects it depends on.
func BuildForwardDependencyGraph(projects []*Project, gitRoot string) map[string][]string {
	absToRel := buildAbsToRel(projects, gitRoot)

	// Build forward dependency graph
	graph := make(map[string][]string)
	for _, p := range projects {
		for _, ref := range p.References {
			if relRef, ok := absToRel[ref]; ok {
				graph[p.Path] = append(graph[p.Path], relRef)
			}
		}
	}
	return graph
}

// GetTransitiveDependencies returns all transitive dependencies of a project.
func GetTransitiveDependencies(projectPath string, forwardGraph map[string][]string) []string {
	visited := make(map[string]bool)
	var result []string

	var visit func(path string)
	visit = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		result = append(result, path)
		for _, dep := range forwardGraph[path] {
			visit(dep)
		}
	}

	// Start from direct dependencies (don't include the project itself)
	for _, dep := range forwardGraph[projectPath] {
		visit(dep)
	}
	return result
}

// FindUntestedProjects returns non-test projects that are not referenced by any test project.
// These projects have no test coverage and should be built (not tested) to at least verify they compile.
func FindUntestedProjects(projects []*Project, forwardGraph map[string][]string) []*Project {
	// Build set of all projects that are referenced by test projects
	testedProjects := make(map[string]bool)
	for _, p := range projects {
		if !p.IsTest {
			continue
		}
		// Add all projects this test project depends on (directly and transitively)
		for _, dep := range GetTransitiveDependencies(p.Path, forwardGraph) {
			testedProjects[dep] = true
		}
	}

	// Find non-test projects that are not in the tested set
	var untested []*Project
	for _, p := range projects {
		if p.IsTest {
			continue
		}
		if !testedProjects[p.Path] {
			untested = append(untested, p)
		}
	}
	return untested
}

// GetRelevantDirs returns the directories that are relevant to a project
// (the project's own directory + directories of all transitive dependencies).
func GetRelevantDirs(project *Project, forwardGraph map[string][]string) []string {
	dirs := map[string]bool{project.Dir: true}

	// Add transitive dependencies
	var visit func(path string)
	visited := make(map[string]bool)
	visit = func(path string) {
		if visited[path] {
			return
		}
		visited[path] = true
		for _, dep := range forwardGraph[path] {
			dirs[filepath.Dir(dep)] = true
			visit(dep)
		}
	}
	visit(project.Path)

	result := make([]string, 0, len(dirs))
	for d := range dirs {
		result = append(result, d)
	}
	return result
}

// FilterFilesToProject filters files to those relevant to a project.
func FilterFilesToProject(files []string, relevantDirs []string) []string {
	var result []string
	for _, f := range files {
		for _, dir := range relevantDirs {
			if strings.HasPrefix(f, dir+"/") || dir == "." {
				result = append(result, f)
				break
			}
		}
	}
	return result
}

// FindAffectedProjects finds all projects affected by changes using the dependency graph.
func FindAffectedProjects(changed map[string]bool, graph map[string][]string, projects []*Project) map[string]bool {
	affected := make(map[string]bool)

	// Copy changed to affected
	for p := range changed {
		affected[p] = true
	}

	// BFS to find all dependents
	queue := make([]string, 0, len(changed))
	for p := range changed {
		queue = append(queue, p)
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, dep := range graph[current] {
			if !affected[dep] {
				affected[dep] = true
				queue = append(queue, dep)
			}
		}
	}

	return affected
}

// FindCompleteSolutionMatches finds solutions where ALL projects in the solution are in the target list.
// Returns: map of solution -> projects in that solution, and slice of projects not matched to any solution.
func FindCompleteSolutionMatches(projects []*Project, solutions []*Solution, gitRoot string) (map[*Solution][]*Project, []*Project) {
	// Build set of absolute project paths -> project
	projectByPath := make(map[string]*Project)
	for _, p := range projects {
		absPath := filepath.Join(gitRoot, p.Path)
		projectByPath[absPath] = p
	}

	// Track which projects are assigned to a solution
	assigned := make(map[string]bool)
	result := make(map[*Solution][]*Project)

	// For each solution, check if ALL its projects are in our target list
	for _, sln := range solutions {
		allInTarget := true
		var slnProjects []*Project
		for projPath := range sln.Projects {
			if p, ok := projectByPath[projPath]; ok {
				slnProjects = append(slnProjects, p)
			} else {
				allInTarget = false
				break
			}
		}
		// Only use solution if ALL its projects need building AND it has 2+ projects
		if allInTarget && len(slnProjects) >= 2 {
			// Check none are already assigned
			anyAssigned := false
			for _, p := range slnProjects {
				if assigned[p.Path] {
					anyAssigned = true
					break
				}
			}
			if !anyAssigned {
				result[sln] = slnProjects
				for _, p := range slnProjects {
					assigned[p.Path] = true
				}
			}
		}
	}

	// Collect remaining unassigned projects
	var remaining []*Project
	for _, p := range projects {
		if !assigned[p.Path] {
			remaining = append(remaining, p)
		}
	}

	return result, remaining
}

// GroupProjectsBySolution groups projects by which solution contains them.
// Prefers solutions that contain more projects.
// Returns: map of solution -> projects in that solution, and slice of projects not in any solution.
func GroupProjectsBySolution(projects []*Project, solutions []*Solution, gitRoot string) (map[*Solution][]*Project, []*Project) {
	// Build set of absolute project paths -> project
	projectByPath := make(map[string]*Project)
	for _, p := range projects {
		absPath := filepath.Join(gitRoot, p.Path)
		projectByPath[absPath] = p
	}

	// Track which projects are assigned to a solution
	assigned := make(map[string]bool)
	result := make(map[*Solution][]*Project)

	// For each solution, find which of our target projects it contains
	// Prefer solutions that contain more projects
	type slnMatch struct {
		sln      *Solution
		projects []*Project
	}
	var matches []slnMatch

	for _, sln := range solutions {
		var slnProjects []*Project
		for projPath := range sln.Projects {
			if p, ok := projectByPath[projPath]; ok {
				slnProjects = append(slnProjects, p)
			}
		}
		if len(slnProjects) >= 2 {
			matches = append(matches, slnMatch{sln: sln, projects: slnProjects})
		}
	}

	// Sort by number of projects (descending) to prefer larger solutions
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].projects) > len(matches[j].projects)
	})

	// Assign projects to solutions, avoiding duplicates
	for _, m := range matches {
		var unassigned []*Project
		for _, p := range m.projects {
			if !assigned[p.Path] {
				unassigned = append(unassigned, p)
			}
		}
		if len(unassigned) >= 2 {
			result[m.sln] = unassigned
			for _, p := range unassigned {
				assigned[p.Path] = true
			}
		}
	}

	// Collect remaining unassigned projects
	var remaining []*Project
	for _, p := range projects {
		if !assigned[p.Path] {
			remaining = append(remaining, p)
		}
	}

	return result, remaining
}

// FindCommonSolution returns a solution that contains all the given projects, or nil if none exists.
func FindCommonSolution(projects []*Project, solutions []*Solution, gitRoot string) *Solution {
	if len(solutions) == 0 || len(projects) == 0 {
		return nil
	}

	// Build set of absolute project paths
	projectPaths := make(map[string]bool)
	for _, p := range projects {
		absPath := filepath.Join(gitRoot, p.Path)
		projectPaths[absPath] = true
	}

	// Find a solution that contains ALL projects
	for _, sln := range solutions {
		allFound := true
		for projPath := range projectPaths {
			if !sln.Projects[projPath] {
				allFound = false
				break
			}
		}
		if allFound {
			return sln
		}
	}

	return nil
}
