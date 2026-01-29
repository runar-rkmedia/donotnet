package cmd

import (
	"runtime/debug"
	"strings"

	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version and build info",
	Long:  `Display version information including git revision and build time.`,
	Run: func(cmd *cobra.Command, args []string) {
		term.Println(versionString())
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func getBuildInfo() (version, vcsRevision, vcsTime, vcsModified string) {
	version = "dev"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			vcsRevision = s.Value[:min(7, len(s.Value))]
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			vcsModified = s.Value
		}
	}
	return
}

func versionString() string {
	version, rev, vcsTime, modified := getBuildInfo()
	parts := []string{"donotnet", version}
	if rev != "" {
		parts = append(parts, rev)
	}
	if modified == "true" {
		parts = append(parts, "(modified)")
	}
	if vcsTime != "" {
		parts = append(parts, "built", vcsTime)
	}
	return strings.Join(parts, " ")
}
