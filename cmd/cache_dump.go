package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
	"github.com/runar-rkmedia/donotnet/project"
	"github.com/runar-rkmedia/donotnet/runner"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var cacheDumpCmd = &cobra.Command{
	Use:   "dump <project>",
	Short: "Dump cached output for a project",
	Long: `Display the cached output for a specific project.

The project can be specified by name or path. Searches all cache entries
for matching project paths.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		cachePath, err := getCachePath()
		if err != nil {
			return err
		}

		db, err := cache.Open(cachePath)
		if err != nil {
			return err
		}
		defer db.Close()

		// Load project scan for computing current content hashes
		scan, scanErr := scanProjects()

		var found bool
		err = db.View(func(key string, entry cache.Entry) error {
			contentHash, argsHash, projectPath := cache.ParseKey(key)
			projectName := filepath.Base(filepath.Dir(projectPath))

			// Match by name or path substring
			if projectName != query && !strings.Contains(projectPath, query) {
				return nil
			}

			found = true
			status := term.ColorGreen + "PASS" + term.ColorReset
			if !entry.Success {
				status = term.ColorRed + "FAIL" + term.ColorReset
			}

			term.Printf("Project:      %s\n", projectPath)
			term.Printf("Cache key:    %s\n", key)
			term.Printf("Content hash: %s\n", contentHash)
			term.Printf("Args hash:    %s\n", argsHash)
			term.Printf("Status:       %s\n", status)
			term.Printf("Args:         %s\n", entry.Args)
			term.Printf("Last run:     %s\n", time.Unix(entry.LastRun, 0).Format(time.RFC3339))

			// Show current content hash comparison if we have scan data
			if scanErr == nil && scan != nil {
				for _, p := range scan.Projects {
					if p.Path == projectPath {
						relevantDirs := project.GetRelevantDirs(p, scan.ForwardGraph)
						currentHash := runner.ComputeContentHash(scan.GitRoot, relevantDirs)
						match := term.ColorGreen + "match" + term.ColorReset
						if currentHash != contentHash {
							match = term.ColorYellow + "changed" + term.ColorReset
						}
						term.Printf("Current hash: %s (%s)\n", currentHash, match)
						break
					}
				}
			}

			if len(entry.Output) > 0 {
				term.Printf("Output size:  %d bytes\n", len(entry.Output))
				term.Printf("\n--- Output ---\n%s\n", string(entry.Output))
			} else {
				term.Dim("(no output stored)")
			}
			term.Println()
			return nil
		})
		if err != nil {
			return err
		}

		if !found {
			return fmt.Errorf("no cache entries found matching %q", query)
		}

		return nil
	},
}

func init() {
	cacheCmd.AddCommand(cacheDumpCmd)
}
