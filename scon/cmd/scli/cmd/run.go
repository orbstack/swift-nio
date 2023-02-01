package cmd

import (
	"os"

	"github.com/alessio/shellescape"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
	"github.com/spf13/cobra"
)

var (
	useShell      bool
	usePath       bool
	flagContainer string
	flagUser      string
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&flagContainer, "container", "c", "", "Use a specific container")
	runCmd.Flags().StringVarP(&flagUser, "user", "u", "", "Run as a specific user")
	runCmd.Flags().BoolVarP(&useShell, "shell", "s", false, "Use the login shell instead of running command directly")
	runCmd.Flags().BoolVarP(&usePath, "path", "p", false, "Translate absolute macOS paths to Linux paths (experimental)")
}

var runCmd = &cobra.Command{
	Use:     "run [flags] -- [COMMAND] [ARGS]...",
	Aliases: []string{"exec", "shell"},
	Short:   "Run command on Linux",
	Long: `Run a command on Linux.

If no arguments are provided, an interactive shell is started.
If container is not specified, the last-used container is used.
If user is not specified, the default user (matching your macOS username) is used.

You can also prefix commands with "` + appid.ShortCmd + `" to run them on Linux. For example:
	` + appid.ShortCmd + ` uname -a
will run "uname -a" on Linux, and is equivalent to: ` + appid.ShortCtl + ` run uname -a
`,
	Example: "  " + appid.ShortCtl + " run ls",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if usePath {
			args = shell.TranslateArgPaths(args)
		}
		if useShell {
			args = []string{shellescape.QuoteCommand(args)}
		}

		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs:  args,
			UseShell:      useShell,
			ContainerName: flagContainer,
			User:          flagUser,
		})
		if err != nil {
			panic(err)
		}

		os.Exit(exitCode)
		return nil
	},
}
