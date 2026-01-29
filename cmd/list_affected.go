package cmd

import (
	"os"
	"path/filepath"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	listAffectedType string
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

		// Find changed projects by checking cache
		argsHash := runner.HashArgs([]string{"test"})
		changed := make(map[string]bool)
		for _, p := range scan.Projects {
			relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
			contentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
			key := cache.MakeKey(contentHash, argsHash, p.Path)
			if flagForce || db.Lookup(key) == nil {
				changed[p.Path] = true
			}
		}

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
			term.Println(p.Name)
		}

		if count == 0 {
			term.Dim("No affected projects")
		}

		return nil
	},
}

func init() {
	listAffectedCmd.Flags().StringVarP(&listAffectedType, "type", "t", "all", "Filter by type: all, tests, non-tests")
	listCmd.AddCommand(listAffectedCmd)
}
