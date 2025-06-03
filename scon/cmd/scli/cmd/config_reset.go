package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
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
	Example:           "  " + rootCmd.Use + " reset",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureVMWithSpinner()
		err := vmclient.Client().ResetConfig()
		checkCLI(err)

		cmd.Println(`Restart OrbStack with "` + rootCmd.Use + ` stop" to apply all changes.`)

		return nil
	},
}
