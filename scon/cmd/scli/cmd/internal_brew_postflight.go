package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	internalCmd.AddCommand(internalBrewPostflight)
}

var internalBrewPostflight = &cobra.Command{
	Use:    "brew-postflight",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if vmclient.IsRunning() {
			scli.EnsureSconVMWithSpinner()
		}

		return nil
	},
}
