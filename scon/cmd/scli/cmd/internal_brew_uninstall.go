package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

var (
	flagZap bool
)

func init() {
	internalCmd.AddCommand(internalBrewUninstall)
	internalBrewUninstall.Flags().BoolVarP(&flagZap, "zap", "z", false, "")
}

var internalBrewUninstall = &cobra.Command{
	Use:    "brew-uninstall",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !vmclient.IsRunning() {
			return nil
		}

		spinner := spinutil.Start("red", "Cleaning up")
		var err error
		if flagZap {
			// force stop if zap
			err = vmclient.Client().SyntheticForceStopOrKill()
		} else {
			// otherwise graceful stop
			err = vmclient.Client().Stop()
		}
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
