package cmd

import (
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
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
	Short: "Stop OrbStack, Docker, and all machines",
	Long: `Stop OrbStack completely. This includes Docker and all Linux machines.

In the future, this will be done automatically if OrbStack hasn't been used for a while.
`,
	Example: "  " + appid.ShortCtl + " shutdown",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !vmclient.IsRunning() {
			cmd.PrintErrln("No machines are running")
			os.Exit(1)
		}

		// spinner
		spinner := spinutil.Start("red", "Stopping Docker and machines")
		var err error
		if flagForce {
			err = vmclient.Client().SyntheticForceStopOrKill()
		} else {
			err = vmclient.Client().Stop()
		}
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
