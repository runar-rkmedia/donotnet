package cmd

import (
	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
)

// FindChangedOpts configures the FindChangedProjects search.
type FindChangedOpts struct {
	Projects     []*project.Project
	ForwardGraph map[string][]string
	GitRoot      string
	DB           *cache.DB
	ArgsHash     string
	VcsFiles     []string // nil = no VCS filtering
	Force        bool
}

// FindChangedProjects returns projects whose content hash is not in the cache.
// Optionally filters by VCS-changed files first.
func FindChangedProjects(opts FindChangedOpts) map[string]bool {
	useVcsFilter := len(opts.VcsFiles) > 0
	changed := make(map[string]bool)

	for _, p := range opts.Projects {
		relevantDirs := project.GetRelevantDirs(p, opts.ForwardGraph)
		if useVcsFilter {
			projectVcsFiles := project.FilterFilesToProject(opts.VcsFiles, relevantDirs)
			if len(projectVcsFiles) == 0 {
				continue
			}
		}

		contentHash := runner.ComputeContentHash(opts.GitRoot, relevantDirs)
		key := cache.MakeKey(contentHash, opts.ArgsHash, p.Path)
		if opts.Force || opts.DB.Lookup(key) == nil {
			changed[p.Path] = true
		}
	}

	return changed
}
