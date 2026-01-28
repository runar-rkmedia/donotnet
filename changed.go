package main

import (
	"sync"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/term"
)

// findChangedProjects returns a map of project paths that have changes
// (either content changes or no cache entry)
func findChangedProjects(db *cache.DB, projects []*Project, root string, argsHash string, forwardGraph map[string][]string, vcsChangedFiles []string, useVcsFilter bool) map[string]bool {
	changed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range projects {
		wg.Add(1)
		go func(p *Project) {
			defer wg.Done()

			// If using VCS filter, first check if this project has VCS changes
			if useVcsFilter {
				relevantDirs := project.GetRelevantDirs(p, forwardGraph)
				projectVcsFiles := project.FilterFilesToProject(vcsChangedFiles, relevantDirs)
				if len(projectVcsFiles) == 0 {
					// No VCS changes for this project, skip entirely
					return
				}
				term.Verbose("  vcs candidate: %s (%d files)", p.Name, len(projectVcsFiles))
				// Fall through to cache check
			}

			if projectChanged(db, p, root, argsHash, forwardGraph) {
				mu.Lock()
				changed[p.Path] = true
				mu.Unlock()
			}
		}(p)
	}

	wg.Wait()
	return changed
}

// projectChanged returns true if a project has changed (content hash mismatch or no cache entry)
func projectChanged(db *cache.DB, p *Project, root string, argsHash string, forwardGraph map[string][]string) bool {
	// Get relevant directories for this project
	relevantDirs := project.GetRelevantDirs(p, forwardGraph)

	// Compute content hash for this project and its dependencies
	contentHash := computeContentHash(root, relevantDirs)

	// Build cache key
	key := cache.MakeKey(contentHash, argsHash, p.Path)

	// Skip cache check if force flag is set
	if *flagForce {
		term.Verbose("  forced: %s (key=%s)", p.Name, key)
		return true
	}

	// Check cache
	if result := db.Lookup(key); result != nil {
		// If --print-output is set, require cached output to exist (only for test projects)
		if *flagPrintOutput && p.IsTest && len(result.Output) == 0 {
			term.Verbose("  cache miss (no output): %s (key=%s)", p.Name, key)
			return true // Treat as changed - need to re-run to capture output
		}
		term.Verbose("  cache hit: %s (key=%s)", p.Name, key)
		return false // Not changed - cache hit
	}

	term.Verbose("  cache miss: %s (key=%s)", p.Name, key)
	return true // Changed - no cache entry
}
