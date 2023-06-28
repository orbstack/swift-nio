package cmd

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/cmd/macctl/shell"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(unlinkCmd)
}

var unlinkCmd = &cobra.Command{
	Use:     "unlink MESSAGE",
	Aliases: []string{"rm-cmd"},
	Short:   "Unlink a macOS command",
	Long: `Remove a link to a command that runs on macOS.
`,
	Example: "  macctl unlink-cmd code",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := shell.UnlinkCmd(args[0])
		if err != nil {
			// print to stderr
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		return nil
	},
}
