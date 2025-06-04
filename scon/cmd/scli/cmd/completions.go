package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate completion script",
	// we pregenerate and automatically load completions for all relevant shells now, so this is no longer useful and may confuse users into thinking that they need to set up completions manually
	Hidden: true,
	Long: fmt.Sprintf(`To load completions:

Bash:

  $ source <(%[1]s completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ %[1]s completion bash > /etc/bash_completion.d/%[1]s
  # macOS:
  $ %[1]s completion bash > $(brew --prefix)/etc/bash_completion.d/%[1]s

Zsh:

  # If shell completion is not already enabled in your environment,
  # you will need to enable it.  You can execute the following once:

  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ orb completion zsh > "${fpath[1]}/_orb"
  $ orbctl completion zsh > "${fpath[1]}/_orbctl"

  # You will need to start a new shell for this setup to take effect.

fish:

  $ %[1]s completion fish | source

  # To load completions for each session, execute once:
  $ %[1]s completion fish > ~/.config/fish/completions/%[1]s.fish

PowerShell:

  PS> %[1]s completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> %[1]s completion powershell > %[1]s.ps1
  # and source this file from your PowerShell profile.
`, rootCmd.Name()),
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	Run: func(cmd *cobra.Command, args []string) {
		gen := func(c *cobra.Command) {
			switch args[0] {
			case "bash":
				c.GenBashCompletion(os.Stdout)
			case "zsh":
				c.GenZshCompletion(os.Stdout)
			case "fish":
				c.GenFishCompletion(os.Stdout, true)
			case "powershell":
				c.GenPowerShellCompletionWithDesc(os.Stdout)
			}

		}

		// we can seemingly only include one command per compinit file: https://github.com/orbstack/macvirt/pull/184
		// so just generate completions for the argv0 we currently are running as
		if args[0] == "zsh" {
			gen(rootCmd)
		} else {
			// twice, so that we get both `orb` and `orbctl` completions
			c := *rootCmd.Root()
			c.Use = "orb"
			gen(&c)
			c.Use = "orbctl"
			gen(&c)
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
