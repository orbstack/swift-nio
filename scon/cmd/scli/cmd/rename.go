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

		err := scli.Client().ContainerRename(oldNameId, newName)
		checkCLI(err)

		return nil
	},
}
