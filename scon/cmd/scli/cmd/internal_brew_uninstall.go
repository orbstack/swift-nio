package cmd

import (
	"os/exec"

	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/vmclient"
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
		// don't panic on errors - do as much as we can
		printErrCLI(err)

		spinner := spinutil.Start("red", "Cleaning up")
		err = vmclient.Client().Stop()
		spinner.Stop()
		printErrCLI(err)

		// uninstall priv helper tool
		vmclient.FindVmgrExe()
		printErrCLI(err)

		return nil
	},
}
