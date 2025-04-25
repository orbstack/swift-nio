package cmd

import (
	"github.com/orbstack/macvirt/vmgr/cmd/macctl/shell"
	"github.com/orbstack/macvirt/vmgr/util/errorx"
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
  - caffeinate
  - code
  - mdfind
  - mdls
  - open
  - osascript
  - pbcopy
  - pbpaste
  - pmset
  - qlmanage
  - screencapture
  - softwareupdate
  - system_profiler
`,
	Example: "  macctl link-cmd code; code .",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := shell.LinkCmd(args[0])
		errorx.CheckCLI(err)

		return nil
	},
}
