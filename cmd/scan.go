package cmd

import (
	"fmt"
	"os"

	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/project"
)

// scanResult holds the results of scanning a .NET project tree.
type scanResult struct {
	GitRoot      string
	ScanRoot     string
	Projects     []*project.Project
	Solutions    []*project.Solution
	Graph        map[string][]string // reverse dependency graph
	ForwardGraph map[string][]string // forward dependency graph
}

// scanProjects discovers projects, solutions, and dependency graphs
// from the current working directory.
func scanProjects() (*scanResult, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	gitRoot, err := git.FindRootFrom(cwd)
	if err != nil {
		return nil, fmt.Errorf("finding git root: %w", err)
	}

	scanRoot := gitRoot
	if cfg != nil && cfg.Local {
		scanRoot = cwd
	}

	projects, err := project.FindProjects(scanRoot, gitRoot)
	if err != nil {
		return nil, fmt.Errorf("finding projects: %w", err)
	}

	solutions, err := project.FindSolutions(scanRoot, gitRoot)
	if err != nil {
		return nil, fmt.Errorf("finding solutions: %w", err)
	}

	return &scanResult{
		GitRoot:      gitRoot,
		ScanRoot:     scanRoot,
		Projects:     projects,
		Solutions:    solutions,
		Graph:        project.BuildDependencyGraph(projects),
		ForwardGraph: project.BuildForwardDependencyGraph(projects),
	}, nil
}
