package cmd

import (
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/spf13/cobra"
)

func init() {
	// currently disabled
	//rootCmd.AddCommand(unlinkCmd)
}

var unlinkCmd = &cobra.Command{
	Use:     "unlink MESSAGE",
	Aliases: []string{"rm-cmd"},
	Short:   "Unlink a Linux command",
	Long: `Remove a link to a command that runs on Linux.

No commands are linked by default.
`,
	Example: "  " + rootCmd.Use + " unlink-cmd code",
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
