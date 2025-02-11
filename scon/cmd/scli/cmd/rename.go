package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(renameCmd)
}

var renameCmd = &cobra.Command{
	Use:   "rename [old name/ID] [new name]",
	Short: "Rename a machine",
	Long: `Rename the specified machine. The old 
`,
	Example: "  " + rootCmd.Use + " rename ubuntu testubuntu",
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

		err = scli.Client().ContainerRename(c.Record, newName)
		checkCLI(err)

		return nil
	},
}
