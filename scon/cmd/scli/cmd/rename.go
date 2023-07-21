package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(renameCmd)
}

var renameCmd = &cobra.Command{
	Use:   "rename [old name/ID] [new name]",
	Short: "Rename a Linux machine",
	Long: `Rename the specified Linux machine. The old 
`,
	Example: "  " + appid.ShortCmd + " rename ubuntu testubuntu",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		oldNameId := args[0]
		newName := args[1]

		// try ID first
		c, err := scli.Client().GetByID(oldNameId)
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(oldNameId)
		}
		checkCLI(err)

		err = scli.Client().ContainerRename(c, newName)
		checkCLI(err)

		return nil
	},
}
