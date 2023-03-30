package cmd

import (
	"os/exec"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	internalCmd.AddCommand(internalBrewUninstall)
}

var internalBrewUninstall = &cobra.Command{
	Use:    "brew-uninstall",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !vmclient.IsRunning() {
			return nil
		}

		// reset docker context
		err := exec.Command(conf.FindXbin("docker"), "context", "use", "default").Run()
		if err != nil {
			return err
		}

		spinner := spinutil.Start("red", "Cleaning up")
		err = vmclient.Client().Stop()
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
