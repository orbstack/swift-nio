package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
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
	Long: `Delete all Linux and Docker data in the VM. All data will be permanently lost!

This resets the virtual machine to its initial state, but configuration is preserved.
All containers will be stopped immediately.
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
		spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		spin.Color("red")
		spin.Suffix = " Resetting data"
		spin.Start()

		err := vmclient.Client().ResetData()
		spin.Stop()
		checkCLI(err)

		return nil
	},
}
