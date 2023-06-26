package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configResetCmd)
}

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset config to defaults",
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
