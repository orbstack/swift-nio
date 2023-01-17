package cmd

import (
	"os"

	"github.com/alessio/shellescape"
	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
	"github.com/spf13/cobra"
)

var (
	useShell bool
	usePath  bool
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVarP(&useShell, "shell", "s", false, "Use the login shell instead of running command directly")
	runCmd.Flags().BoolVarP(&usePath, "path", "p", false, "Translate absolute Linux paths to macOS paths (experimental)")
}

var runCmd = &cobra.Command{
	Use:     "run [COMMAND] [ARGS]...",
	Aliases: []string{"exec", "shell"},
	Short:   "Run command on macOS",
	Long: `Run a command on macOS.

If no arguments are provided, an interactive shell is started.`,
	Example: "  macctl run ls",
	Args:    cobra.MatchAll(cobra.ArbitraryArgs, cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		if usePath {
			args = shell.TranslateArgPaths(args)
		}
		if useShell {
			args = []string{shellescape.QuoteCommand(args)}
		}

		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs: args,
			UseShell:     useShell,
		})
		if err != nil {
			panic(err)
		}

		os.Exit(exitCode)
		return nil
	},
}
