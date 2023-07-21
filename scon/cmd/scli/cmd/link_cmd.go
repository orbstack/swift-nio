package cmd

import (
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	// currently disabled
	//rootCmd.AddCommand(linkCmd)
}

var linkCmd = &cobra.Command{
	Use:     "link MESSAGE",
	Aliases: []string{"add-cmd"},
	Short:   "Link a command to macOS",
	Long: `Create a link to a command that runs on macOS.

This makes a specific macOS command available to run directly from Linux, without prefixing it with ` + appid.ShortCmd + ` or ` + appid.ShortCmd + `.
To remove a linked command, use "` + appid.ShortCmd + ` unlink".

No commands are linked by default.
`,
	Example: "  " + appid.ShortCmd + " link-cmd code; code .",
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
