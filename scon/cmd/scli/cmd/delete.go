package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
}

var deleteCmd = &cobra.Command{
	Use:   "delete [ID/NAME]",
	Short: "Delete a Linux machine",
	Long: `Delete the specified Linux machine, by ID or name.

The machine will be stopped if it is running.
All files stored in the machine will be PERMANENTLY LOST without warning!
`,
	Example: "  " + appid.ShortCtl + " delete ubuntu",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		checkCLI(err)

		// spinner
		spinner := spinutil.Start("red", "Deleting "+c.Name)
		err = scli.Client().ContainerDelete(c)
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
