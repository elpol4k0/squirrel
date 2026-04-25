package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:       "completion [bash|zsh|fish|powershell]",
	Short:     "Generate shell completion script",
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	Args:      cobra.ExactArgs(1),
	Example: `  squirrel completion bash > /etc/bash_completion.d/squirrel
  squirrel completion zsh > "${fpath[1]}/_squirrel"
  squirrel completion fish > ~/.config/fish/completions/squirrel.fish
  squirrel completion powershell >> $PROFILE`,
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletionV2(w, true)
		case "zsh":
			return rootCmd.GenZshCompletion(w)
		case "fish":
			return rootCmd.GenFishCompletion(w, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(w)
		default:
			return fmt.Errorf("unknown shell %q – choose: bash, zsh, fish, powershell", args[0])
		}
	},
}
