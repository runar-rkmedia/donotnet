package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/runar-rkmedia/donotnet/cache"
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

		var found bool
		err = db.View(func(key string, entry cache.Entry) error {
			_, _, projectPath := cache.ParseKey(key)
			projectName := filepath.Base(filepath.Dir(projectPath))

			// Match by name or path substring
			if projectName != query && !strings.Contains(projectPath, query) {
				return nil
			}

			found = true
			status := "PASS"
			if !entry.Success {
				status = "FAIL"
			}

			term.Printf("Project: %s\n", projectPath)
			term.Printf("Status:  %s\n", status)
			term.Printf("Args:    %s\n", entry.Args)
			term.Printf("Last run: %s\n", time.Unix(entry.LastRun, 0).Format(time.RFC3339))
			if len(entry.Output) > 0 {
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
