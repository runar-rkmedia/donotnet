package main

import (
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// watchContext holds all the state needed for watch mode
type watchContext struct {
	command          string
	dotnetArgs       []string
	gitRoot          string
	db               *cache.DB
	projects         []*Project
	solutions        []*Solution
	graph            map[string][]string // reverse dependency graph
	forwardGraph     map[string][]string
	projectsByPath   map[string]*Project
	projectsByDir    map[string]*Project // maps directory to project
	reportsDir       string
	argsHash         string
	testFilter       *TestFilter                 // tracks changed files for smart test filtering
	coverageMap      *coverage.Map               // maps source files to test projects that cover them
	testCoverageMaps map[string]*TestCoverageMap // per-test coverage maps for test filtering
}

// relevantExtensions are file extensions we care about for watch mode
var relevantExtensions = map[string]bool{
	".cs":      true,
	".csproj":  true,
	".razor":   true,
	".props":   true,
	".targets": true,
}

func runWatchMode(ctx *watchContext) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		term.Errorf("creating watcher: %v", err)
		os.Exit(1)
	}
	defer watcher.Close()

	// Add all project directories to watcher
	watchedDirs := make(map[string]bool)
	for _, p := range ctx.projects {
		projectDir := filepath.Join(ctx.gitRoot, p.Dir)
		if err := addDirRecursive(watcher, projectDir, watchedDirs); err != nil {
			term.Verbose("warning: failed to watch %s: %v", projectDir, err)
		}
	}

	term.Info("Watching %d directories for changes (Ctrl+C to stop)...", len(watchedDirs))

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)

	// Debounce timer
	var debounceTimer *time.Timer
	pendingChanges := make(map[string]bool)   // project paths with pending changes
	pendingFiles := make(map[string]struct{}) // changed file paths (relative to gitRoot)
	var pendingMu sync.Mutex

	// Initialize test filter if not already done
	if ctx.testFilter == nil {
		ctx.testFilter = NewTestFilter()
		ctx.testFilter.SetCoverageMaps(ctx.testCoverageMaps)
		ctx.testFilter.SetHeuristics(ParseHeuristics(*flagHeuristics))
	}

	runPending := func() {
		pendingMu.Lock()
		if len(pendingChanges) == 0 && len(pendingFiles) == 0 {
			pendingMu.Unlock()
			return
		}

		// Collect changed projects and files
		changedProjects := make(map[string]bool)
		for p := range pendingChanges {
			changedProjects[p] = true
		}
		changedFiles := make([]string, 0, len(pendingFiles))
		for f := range pendingFiles {
			changedFiles = append(changedFiles, f)
		}
		pendingChanges = make(map[string]bool)
		pendingFiles = make(map[string]struct{})

		// Copy test filter and clear for next batch (preserving coverage maps and heuristics)
		testFilter := ctx.testFilter
		ctx.testFilter = NewTestFilter()
		ctx.testFilter.SetCoverageMaps(ctx.testCoverageMaps)
		ctx.testFilter.SetHeuristics(ParseHeuristics(*flagHeuristics))
		pendingMu.Unlock()

		// Determine target test projects using coverage map or fallback
		var targetProjects []*Project
		usedCoverage := false

		if ctx.command == "test" && ctx.coverageMap != nil && ctx.coverageMap.HasCoverage() {
			// Try coverage-based selection
			coveredTestProjects := make(map[string]bool)
			uncoveredFiles := []string{}

			for _, f := range changedFiles {
				testProjs := ctx.coverageMap.GetTestProjectsForFile(f)
				if len(testProjs) > 0 {
					for _, tp := range testProjs {
						coveredTestProjects[tp] = true
					}
				} else {
					uncoveredFiles = append(uncoveredFiles, f)
				}
			}

			if len(uncoveredFiles) == 0 && len(coveredTestProjects) > 0 {
				// All files are covered - use coverage-based selection
				usedCoverage = true
				for _, p := range ctx.projects {
					if coveredTestProjects[p.Path] {
						targetProjects = append(targetProjects, p)
					}
				}
				term.Verbose("  coverage-based: %d test projects for %d files",
					len(targetProjects), len(changedFiles))
			} else if len(uncoveredFiles) > 0 {
				// Some files not in coverage map - will fall back
				term.Verbose("  uncovered files: %v", uncoveredFiles)
			}
		}

		if !usedCoverage {
			// Fallback: use dependency-based selection
			affected := project.FindAffectedProjects(changedProjects, ctx.graph, ctx.projects)

			for _, p := range ctx.projects {
				if ctx.command == "test" && !p.IsTest {
					continue
				}
				if affected[p.Path] {
					targetProjects = append(targetProjects, p)
				}
			}
			if ctx.coverageMap != nil {
				term.Verbose("  fallback to dependencies: %d projects", len(targetProjects))
			}
		}

		if len(targetProjects) == 0 {
			return
		}

		term.Println()

		runDotnetCommand(ctx.command, targetProjects, ctx.dotnetArgs, ctx.gitRoot, ctx.db, ctx.argsHash, ctx.forwardGraph, ctx.projectsByPath, nil, ctx.reportsDir, testFilter, nil, ctx.solutions, nil)

		term.Info("\nWatching for changes...")
	}

	for {
		select {
		case <-sigChan:
			term.Dim("\nStopping watch mode...")
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Only care about writes and creates
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			// Check if it's a relevant file
			ext := strings.ToLower(filepath.Ext(event.Name))
			if !relevantExtensions[ext] {
				continue
			}

			// Find which project this file belongs to
			relPath, err := filepath.Rel(ctx.gitRoot, event.Name)
			if err != nil {
				continue
			}

			var affectedProject *Project
			for _, p := range ctx.projects {
				if strings.HasPrefix(relPath, p.Dir+"/") || strings.HasPrefix(relPath, p.Dir+"\\") {
					affectedProject = p
					break
				}
			}

			if affectedProject == nil {
				continue
			}

			term.Verbose("  changed: %s (%s)", relPath, affectedProject.Name)

			pendingMu.Lock()
			pendingChanges[affectedProject.Path] = true
			pendingFiles[relPath] = struct{}{}
			ctx.testFilter.AddChangedFile(affectedProject.Path, relPath)
			pendingMu.Unlock()

			// Debounce - wait 100ms for more changes before running
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(100*time.Millisecond, runPending)

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			term.Errorf("watcher: %v", err)
		}
	}
}

func addDirRecursive(watcher *fsnotify.Watcher, dir string, watched map[string]bool) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		// Skip non-project directories
		if name == "bin" || name == "obj" || name == ".git" || name == "node_modules" || name == ".vs" {
			return filepath.SkipDir
		}

		if watched[path] {
			return nil
		}

		if err := watcher.Add(path); err != nil {
			return err
		}
		watched[path] = true
		return nil
	})
}
