package cmd

import (
	"os"
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/spf13/cobra"
)

var (
	flagForce bool
)

func init() {
	rootCmd.AddCommand(shutdownCmd)
	shutdownCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force VM to stop immediately. May cause data loss!")
}

var shutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Stop the lightweight Linux virtual machine",
	Long: `Stop the lightweight Linux virtual machine. This will stop Docker and all Linux containers.

In the future, this will be done automatically if the VM is idle and unused.
`,
	Example: "  " + appid.ShortCtl + " shutdown",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !vmclient.IsRunning() {
			cmd.PrintErrln("VM is not running")
			os.Exit(1)
		}

		// spinner
		spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		spin.Color("red")
		spin.Suffix = " Stopping VM and containers"
		spin.Start()

		var err error
		if flagForce {
			err = vmclient.Client().ForceStop()
		} else {
			err = vmclient.Client().Stop()
		}
		spin.Stop()

		// EOF is ok, it means we got disconnected
		// TODO fix
		if err != nil && err.Error() != `[-32603] Post "http://vmrpc": EOF` {
			checkCLI(err)
		}

		return nil
	},
}
