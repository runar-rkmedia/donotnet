package cmd

import (
	"os"
	"path/filepath"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	listAffectedType   string
	listAffectedVcsRef string
)

var listAffectedCmd = &cobra.Command{
	Use:   "affected",
	Short: "List affected projects",
	Long: `List projects that have been affected by changes.

Projects can be filtered by type:
  all       - All affected projects (default)
  tests     - Only test projects
  non-tests - Only non-test projects`,
	RunE: func(cmd *cobra.Command, args []string) error {
		scan, err := scanProjects()
		if err != nil {
			return err
		}

		// Use uncommitted changes to determine affected projects,
		// matching the old behavior where list-affected implied VCS-changed mode.
		vcsChangedFiles := git.GetDirtyFiles(scan.GitRoot)
		if listAffectedVcsRef != "" {
			vcsChangedFiles, err = git.GetChangedFiles(scan.GitRoot, listAffectedVcsRef)
			if err != nil {
				return err
			}
		}
		// Open cache to find changed projects
		cacheDir := ""
		if cfg != nil && cfg.CacheDir != "" {
			cacheDir = cfg.CacheDir
		} else {
			cacheDir = filepath.Join(scan.GitRoot, ".donotnet")
		}
		os.MkdirAll(cacheDir, 0755)
		cachePath := filepath.Join(cacheDir, "cache.db")

		db, err := cache.Open(cachePath)
		if err != nil {
			return err
		}
		defer db.Close()

		// Find changed projects by checking cache + VCS filter
		changed := FindChangedProjects(FindChangedOpts{
			Projects:     scan.Projects,
			ForwardGraph: scan.ForwardGraph,
			GitRoot:      scan.GitRoot,
			DB:           db,
			ArgsHash:     runner.HashArgs([]string{"test"}),
			VcsFiles:     vcsChangedFiles,
			Force:        flagForce,
		})

		affected := project.FindAffectedProjects(changed, scan.Graph, scan.Projects)

		var count int
		for _, p := range scan.Projects {
			if !affected[p.Path] {
				continue
			}
			switch listAffectedType {
			case "tests":
				if !p.IsTest {
					continue
				}
			case "non-tests":
				if p.IsTest {
					continue
				}
			}
			count++
			term.Println(p.Path)
		}

		if count == 0 {
			term.Dim("No affected projects")
		}

		return nil
	},
}

func init() {
	listAffectedCmd.Flags().StringVarP(&listAffectedType, "type", "t", "all", "Filter by type: all, tests, non-tests")
	listAffectedCmd.Flags().StringVar(&listAffectedVcsRef, "vcs-ref", "", "Compare against a git ref (e.g., main, HEAD~3) instead of uncommitted changes")
	listCmd.AddCommand(listAffectedCmd)
}
