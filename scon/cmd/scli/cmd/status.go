package cmd

import (
	"fmt"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check whether OrbStack is running",
	Long: `Check whether OrbStack is running.

Returns one of the following statuses and exit codes:
  Running: status 0
  Starting: status 2
  Stopped: status 1
`,
	Example: "  " + appid.ShortCtl + " status",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if vmclient.IsRunning() {
			isSconRunning, err := vmclient.IsSconRunning()
			checkCLI(err)
			if !isSconRunning {
				fmt.Println("Starting")
				os.Exit(2)
			}

			fmt.Println("Running")
			return nil
		} else {
			fmt.Println("Stopped")
			os.Exit(1)
		}

		return nil
	},
}
