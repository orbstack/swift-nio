package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
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

This resets OrbStack to its initial state, but configuration is preserved.
All machines will be stopped immediately.
`,
	Example: "  " + appid.ShortCmd + " reset",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// confirm
		if !flagYes {
			cmd.PrintErrln("WARNING: This will PERMANENTLY DELETE ALL DOCKER AND LINUX DATA!")
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

		spinner := spinutil.Start("red", "Resetting data")
		var err error
		if vmclient.IsRunning() {
			err = vmclient.Client().ResetData()
		} else {
			err = os.RemoveAll(conf.DataDir())
		}
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
