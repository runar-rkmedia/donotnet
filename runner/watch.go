package runner

import (
	"context"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/runar-rkmedia/donotnet/coverage"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/runar-rkmedia/donotnet/testfilter"
)

// ignoredExtensions are file extensions we know are not relevant for watch mode.
// Everything else is assumed to potentially affect builds/tests.
var ignoredExtensions = map[string]bool{
	".dll":  true,
	".exe":  true,
	".pdb":  true,
	".obj":  true,
	".o":    true,
	".a":    true,
	".so":   true,
	".dylib": true,
	".nupkg": true,
	".snupkg": true,
	".log":  true,
	".suo":  true,
	".user": true,
	".cache": true,
	".tmp":  true,
	".swp":  true,
	".swo":  true,
}

// runWatch sets up file watchers and re-runs on file changes.
// The initial run is handled by Run() before calling this.
func (r *Runner) runWatch(ctx context.Context, targets []*project.Project, argsHash string) error {
	// Build coverage map for test project selection
	var covMap *coverage.Map
	if r.opts.Command == "test" {
		covMap = buildCoverageMap(r.gitRoot, r.projects)
		if covMap != nil {
			term.Verbose("Coverage map: %d test projects with coverage, %d files mapped",
				len(covMap.TestProjectToFiles), len(covMap.FileToTestProjects))
			if len(covMap.MissingTestProjects) > 0 {
				term.Verbose("  Missing coverage: %d projects", len(covMap.MissingTestProjects))
			}
			if len(covMap.StaleTestProjects) > 0 {
				term.Verbose("  Stale coverage: %d projects", len(covMap.StaleTestProjects))
			}
		}
	}

	// Load per-test coverage maps for test filtering
	testCovMaps := loadAllTestCoverageMaps(r.cacheDir)
	if len(testCovMaps) > 0 {
		term.Verbose("Loaded per-test coverage for %d project(s)", len(testCovMaps))
	}

	// Set up initial test filter
	tf := testfilter.NewTestFilter()
	tf.SetCoverageMaps(testCovMaps)
	tf.SetHeuristics(testfilter.ParseHeuristics(r.opts.Heuristics))

	// Set up fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	watchedDirs := make(map[string]bool)
	for _, p := range r.projects {
		projectDir := filepath.Join(r.gitRoot, p.Dir)
		if addErr := addDirRecursive(watcher, projectDir, watchedDirs); addErr != nil {
			term.Verbose("warning: failed to watch %s: %v", projectDir, addErr)
		}
	}

	term.Info("Watching %d directories for changes (Ctrl+C to stop)...", len(watchedDirs))

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals...)
	defer signal.Stop(sigChan)

	// Debounce state
	var debounceTimer *time.Timer
	pendingChanges := make(map[string]bool)
	pendingFiles := make(map[string]struct{})
	var pendingMu sync.Mutex

	runPending := func() {
		pendingMu.Lock()
		if len(pendingChanges) == 0 && len(pendingFiles) == 0 {
			pendingMu.Unlock()
			return
		}

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

		// Take the current test filter and set up a fresh one for the next batch
		currentFilter := tf
		tf = testfilter.NewTestFilter()
		tf.SetCoverageMaps(testCovMaps)
		tf.SetHeuristics(testfilter.ParseHeuristics(r.opts.Heuristics))
		pendingMu.Unlock()

		// Determine target test projects
		var watchTargets []*project.Project
		usedCoverage := false

		if r.opts.Command == "test" && covMap != nil && covMap.HasCoverage() {
			coveredTestProjects := make(map[string]bool)
			var uncoveredFiles []string

			for _, f := range changedFiles {
				testProjs := covMap.GetTestProjectsForFile(f)
				if len(testProjs) > 0 {
					for _, tp := range testProjs {
						coveredTestProjects[tp] = true
					}
				} else {
					uncoveredFiles = append(uncoveredFiles, f)
				}
			}

			if len(uncoveredFiles) == 0 && len(coveredTestProjects) > 0 {
				usedCoverage = true
				for _, p := range r.projects {
					if coveredTestProjects[p.Path] {
						watchTargets = append(watchTargets, p)
					}
				}
				term.Verbose("  coverage-based: %d test projects for %d files",
					len(watchTargets), len(changedFiles))
			} else if len(uncoveredFiles) > 0 {
				term.Verbose("  uncovered files: %v", uncoveredFiles)
			}
		}

		if !usedCoverage {
			affected := project.FindAffectedProjects(changedProjects, r.graph, r.projects)
			for _, p := range r.projects {
				if r.opts.Command == "test" && !p.IsTest {
					continue
				}
				if affected[p.Path] {
					watchTargets = append(watchTargets, p)
				}
			}
			if covMap != nil {
				term.Verbose("  fallback to dependencies: %d projects", len(watchTargets))
			}
		}

		if len(watchTargets) == 0 {
			return
		}

		term.Println()

		// Set the test filter on opts for this run
		r.opts.TestFilter = currentFilter

		r.runProjects(ctx, watchTargets, nil, argsHash)

		term.Info("\nWatching for changes...")
	}

	for {
		select {
		case <-sigChan:
			term.Dim("\nStopping watch mode...")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}

			ext := strings.ToLower(filepath.Ext(event.Name))
			if ignoredExtensions[ext] {
				continue
			}

			relPath, relErr := filepath.Rel(r.gitRoot, event.Name)
			if relErr != nil {
				continue
			}

			var affectedProject *project.Project
			for _, p := range r.projects {
				if strings.HasPrefix(relPath, p.Dir+"/") || strings.HasPrefix(relPath, p.Dir+string(os.PathSeparator)) {
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
			tf.AddChangedFile(affectedProject.Path, relPath)
			pendingMu.Unlock()

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(100*time.Millisecond, runPending)

		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			term.Errorf("watcher: %v", watchErr)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// buildCoverageMap builds a coverage.Map from the project list.
func buildCoverageMap(gitRoot string, projects []*project.Project) *coverage.Map {
	var testProjects []coverage.TestProject
	for _, p := range projects {
		if p.IsTest {
			testProjects = append(testProjects, coverage.TestProject{
				Path: p.Path,
				Dir:  p.Dir,
			})
		}
	}
	if len(testProjects) == 0 {
		return nil
	}
	return coverage.BuildMap(gitRoot, testProjects)
}

// loadAllTestCoverageMaps loads per-test coverage maps from the cache directory.
func loadAllTestCoverageMaps(cacheDir string) map[string]*testfilter.TestCoverageMap {
	result := make(map[string]*testfilter.TestCoverageMap)

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return result
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".testcoverage.json") {
			continue
		}

		path := filepath.Join(cacheDir, name)
		covMap, loadErr := testfilter.LoadTestCoverageMap(path)
		if loadErr != nil {
			term.Verbose("  failed to load %s: %v", name, loadErr)
			continue
		}

		projectName := strings.TrimSuffix(name, ".testcoverage.json")
		result[projectName] = covMap
	}

	return result
}

// addDirRecursive adds a directory and all subdirectories to the watcher,
// skipping build output and VCS directories.
func addDirRecursive(watcher *fsnotify.Watcher, dir string, watched map[string]bool) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		if project.ShouldSkipDir(d.Name()) {
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
