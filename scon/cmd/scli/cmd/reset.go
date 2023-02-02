package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

var (
	flagYes bool
)

func init() {
	rootCmd.AddCommand(resetCmd)
	resetCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt")
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete all Linux and Docker data",
	Long: `Delete all Linux machines and Docker data. All data will be permanently lost!

This resets the lightweight VM to its initial state, but configuration is preserved.
All machines will be stopped immediately.
`,
	Example: "  " + appid.ShortCtl + " reset",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// confirm
		if !flagYes {
			cmd.PrintErrln("WARNING: This will PERMANENTLY DELETE ALL DATA in the VM!")
			cmd.PrintErrln("This includes all Linux and Docker data.")
			cmd.PrintErrln("This cannot be undone.")
			cmd.PrintErrln("")
			cmd.PrintErr("Are you sure you want to continue [y/N]? ")
			var resp string
			_, err := fmt.Scanln(&resp)
			checkCLI(err)
			lower := strings.ToLower(resp)
			if lower != "y" && lower != "yes" {
				cmd.PrintErrln("Aborted")
				os.Exit(1)
			}
		}

		// spinner
		spinner := spinutil.Start("red", "Resetting data")
		err := vmclient.Client().ResetData()
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
