package cmd

import (
	"encoding/json"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/runar-rkmedia/donotnet/config"
	"github.com/runar-rkmedia/donotnet/git"
	"github.com/runar-rkmedia/donotnet/term"
	"github.com/spf13/cobra"
)

var (
	configFlagFormat    string
	configFlagLocations bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show effective configuration",
	Long: `Display the effective configuration after merging all sources.

Shows the final configuration values that will be used, combining:
- Default values
- User config (~/.config/donotnet/config.toml)
- Parent directory configs
- Git root config
- Current directory config
- Environment variables (DONOTNET_*)
- Command-line flags`,
	RunE: runConfig,
}

func init() {
	configCmd.Flags().StringVarP(&configFlagFormat, "format", "f", "toml", "Output format: toml, json, yaml")
	configCmd.Flags().BoolVar(&configFlagLocations, "locations", false, "Show config file locations instead of values")
	rootCmd.AddCommand(configCmd)
}

func runConfig(cmd *cobra.Command, args []string) error {
	if configFlagLocations {
		return showLocations()
	}

	return showConfig()
}

func showLocations() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	gitRoot, _ := git.FindRootFrom(cwd)
	locations := config.FindLocations(cwd, gitRoot)

	term.Println("Config file locations (in merge order):")
	term.Println("")

	for _, loc := range locations {
		status := term.ColorDim + "(not found)" + term.ColorReset
		if loc.Exists {
			status = term.ColorGreen + "(found)" + term.ColorReset
		}

		if term.IsPlain() {
			if loc.Exists {
				status = "(found)"
			} else {
				status = "(not found)"
			}
		}

		term.Printf("  [%s] %s %s\n", loc.Source, loc.Path, status)
	}

	term.Println("")
	term.Println("Environment variables: DONOTNET_* (e.g., DONOTNET_VERBOSE, DONOTNET_TEST_COVERAGE)")

	return nil
}

func showConfig() error {
	c := GetConfig()
	if c == nil {
		c = config.Default()
	}

	var output []byte
	var err error

	switch configFlagFormat {
	case "json":
		output, err = json.MarshalIndent(c, "", "  ")
	case "yaml":
		// For YAML, we use JSON encoding with different formatting
		// (go-yaml would need another dependency)
		output, err = json.MarshalIndent(c, "", "  ")
		if err == nil {
			term.Println("# YAML output (using JSON format - install yq for proper YAML):")
		}
	default: // toml
		output, err = toml.Marshal(c)
	}

	if err != nil {
		return err
	}

	term.Println(string(output))
	return nil
}
