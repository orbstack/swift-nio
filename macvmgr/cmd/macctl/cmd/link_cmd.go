package cmd

import (
	"os"

	"github.com/orbstack/macvirt/macvmgr/cmd/macctl/shell"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(linkCmd)
}

var linkCmd = &cobra.Command{
	Use:     "link MESSAGE",
	Aliases: []string{"add-cmd"},
	Short:   "Link a command to macOS",
	Long: `Create a link to a command that runs on macOS.

This makes a specific macOS command available to run directly from Linux, without prefixing it with macctl or mac.
To remove a linked command, use "macctl unlink".

The following commands are linked by default and cannot be unlinked:
  - open
  - osascript
  - code
`,
	Example: "  macctl link-cmd code; code .",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := shell.LinkCmd(args[0])
		if err != nil {
			// print to stderr
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		return nil
	},
}
