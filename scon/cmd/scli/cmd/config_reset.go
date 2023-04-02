package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configResetCmd)
}

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset configuration to default",
	Long: `Reset all configuration options to their default values.

Some options will only take effect after restarting the virtual machine.
`,
	Example: "  " + appid.ShortCtl + " reset",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureVMWithSpinner()
		err := vmclient.Client().ResetConfig()
		checkCLI(err)

		cmd.Println(`Restart OrbStack with "` + appid.ShortCtl + ` shutdown" to apply all changes.`)

		return nil
	},
}
