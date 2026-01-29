package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for donotnet.

To load completions:

Bash:
  $ source <(donotnet completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ donotnet completion bash > /etc/bash_completion.d/donotnet
  # macOS:
  $ donotnet completion bash > $(brew --prefix)/etc/bash_completion.d/donotnet

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ donotnet completion zsh > "${fpath[1]}/_donotnet"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ donotnet completion fish | source

  # To load completions for each session, execute once:
  $ donotnet completion fish > ~/.config/fish/completions/donotnet.fish

PowerShell:
  PS> donotnet completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> donotnet completion powershell > donotnet.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
