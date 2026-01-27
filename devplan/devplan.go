// Package devplan provides job scheduling plan visualization for donotnet.
// It shows how projects would be scheduled based on their dependencies.
package devplan

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Project represents the minimal project info needed for planning
type Project struct {
	Path string
	Name string
}

// Wave represents a group of projects that can run in parallel
type Wave struct {
	Number   int
	Projects []WaveProject
}

// WaveProject is a project in a wave with its dependency info
type WaveProject struct {
	Name         string
	Dependencies []string // names of dependencies within the target set
}

// Plan represents the computed scheduling plan
type Plan struct {
	Waves          []Wave
	TotalProjects  int
	HasCycleError  bool
	StuckProjects  []StuckProject
}

// StuckProject represents a project stuck in a circular dependency
type StuckProject struct {
	Name       string
	WaitingOn  []string
}

// ComputePlan calculates the scheduling plan based on dependencies
func ComputePlan(projects []*Project, forwardGraph map[string][]string) *Plan {
	// Build target set
	targetSet := make(map[string]bool)
	projectByPath := make(map[string]*Project)
	for _, p := range projects {
		targetSet[p.Path] = true
		projectByPath[p.Path] = p
	}

	// For each project, find deps within target set
	pendingDeps := make(map[string][]string)
	for _, p := range projects {
		var deps []string
		for _, depPath := range forwardGraph[p.Path] {
			if targetSet[depPath] {
				deps = append(deps, depPath)
			}
		}
		pendingDeps[p.Path] = deps
	}

	// Simulate scheduling waves
	completed := make(map[string]bool)
	waveNum := 1
	var waves []Wave

	for len(completed) < len(projects) {
		// Find ready projects (all deps completed)
		var ready []*Project
		for _, p := range projects {
			if completed[p.Path] {
				continue
			}
			isReady := true
			for _, dep := range pendingDeps[p.Path] {
				if !completed[dep] {
					isReady = false
					break
				}
			}
			if isReady {
				ready = append(ready, p)
			}
		}

		if len(ready) == 0 {
			// Circular dependency detected
			var stuck []StuckProject
			for _, p := range projects {
				if !completed[p.Path] {
					var waitingOn []string
					for _, dep := range pendingDeps[p.Path] {
						if !completed[dep] {
							if dp := projectByPath[dep]; dp != nil {
								waitingOn = append(waitingOn, dp.Name)
							} else {
								waitingOn = append(waitingOn, filepath.Base(filepath.Dir(dep)))
							}
						}
					}
					stuck = append(stuck, StuckProject{Name: p.Name, WaitingOn: waitingOn})
				}
			}
			return &Plan{
				Waves:         waves,
				TotalProjects: len(projects),
				HasCycleError: true,
				StuckProjects: stuck,
			}
		}

		wave := Wave{Number: waveNum}
		for _, p := range ready {
			deps := pendingDeps[p.Path]
			var depNames []string
			for _, dep := range deps {
				if dp := projectByPath[dep]; dp != nil {
					depNames = append(depNames, dp.Name)
				} else {
					depNames = append(depNames, filepath.Base(filepath.Dir(dep)))
				}
			}
			wave.Projects = append(wave.Projects, WaveProject{
				Name:         p.Name,
				Dependencies: depNames,
			})
			completed[p.Path] = true
		}
		waves = append(waves, wave)
		waveNum++
	}

	return &Plan{
		Waves:         waves,
		TotalProjects: len(projects),
	}
}

// Colors for terminal output
type Colors struct {
	Reset  string
	Red    string
	Green  string
	Yellow string
	Cyan   string
	Dim    string
	Bold   string
}

// DefaultColors returns ANSI color codes
func DefaultColors() Colors {
	return Colors{
		Reset:  "\033[0m",
		Red:    "\033[31m",
		Green:  "\033[32m",
		Yellow: "\033[33m",
		Cyan:   "\033[36m",
		Dim:    "\033[2m",
		Bold:   "\033[1m",
	}
}

// PlainColors returns empty strings (no colors)
func PlainColors() Colors {
	return Colors{}
}

// Print outputs the plan to the given writer
func (p *Plan) Print(w io.Writer, c Colors) {
	fmt.Fprintf(w, "%s%sJob Scheduling Plan%s (%d projects)\n", c.Bold, c.Cyan, c.Reset, p.TotalProjects)
	fmt.Fprintln(w, strings.Repeat("─", 50))
	fmt.Fprintf(w, "%sWaves show dependency order. In practice, projects start\nas soon as their dependencies complete, not in strict waves.%s\n", c.Dim, c.Reset)

	for _, wave := range p.Waves {
		fmt.Fprintf(w, "\n%s%sWave %d%s %s(%d projects, can run in parallel)%s\n",
			c.Bold, c.Green, wave.Number, c.Reset, c.Dim, len(wave.Projects), c.Reset)

		for _, proj := range wave.Projects {
			if len(proj.Dependencies) == 0 {
				fmt.Fprintf(w, "  %s•%s %s\n", c.Green, c.Reset, proj.Name)
			} else {
				fmt.Fprintf(w, "  %s•%s %s %s(after: %s)%s\n",
					c.Yellow, c.Reset, proj.Name, c.Dim, strings.Join(proj.Dependencies, ", "), c.Reset)
			}
		}
	}

	if p.HasCycleError {
		fmt.Fprintf(w, "\n%sERROR:%s Circular dependency detected!\n", c.Red, c.Reset)
		for _, stuck := range p.StuckProjects {
			fmt.Fprintf(w, "  %sStuck:%s %s (waiting on: %v)\n", c.Red, c.Reset, stuck.Name, stuck.WaitingOn)
		}
	}

	fmt.Fprintln(w)
}
