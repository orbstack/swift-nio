package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/completions"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(cloneCmd)
}

var cloneCmd = &cobra.Command{
	GroupID: groupMachines,
	Use:     "clone [OLD_NAME] [NEW_NAME]",
	Short:   "Clone a machine",
	Long: `Make a copy of an existing machine.

The new machine will have all the data and settings from the old machine. Changes in the new machine will not affect the old one.

Data is snapshotted and copied on demand, so cloning a machine does not result in double the disk usage.

The new machine will be in a stopped state.

To prevent data corruption, the existing machine will be paused while cloning.
`,
	Example:           `  orb clone ubuntu foo`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completions.Limit(2, completions.Machines),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldNameOrID := args[0]
		newName := args[1]

		scli.EnsureSconVMWithSpinner()

		// spinner
		spinner := spinutil.Start("blue", "Cloning "+oldNameOrID)
		_, err := scli.Client().ContainerClone(oldNameOrID, newName)
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
