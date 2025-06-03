package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/completions"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(defaultCmd)
}

var defaultCmd = &cobra.Command{
	GroupID: groupMachines,
	Use:     "default [NAME/ID]",
	Aliases: []string{"set-default", "get-default"},
	Short:   "Get or set the default machine",
	Long: `Get or set the default machine used for commands.

Use "orb config" to change the default username.
`,
	Example:           "  " + rootCmd.Use + " set-default ubuntu",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completions.Limit(1, completions.Machines),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		if len(args) == 0 {
			// get
			c, err := scli.Client().GetDefaultContainer()
			checkCLI(err)
			if c == nil || c.ID == "" {
				cmd.PrintErrln("No default machine set. (will pick most recently-used machine)")
			} else {
				fmt.Println(c.Name)
			}
		} else {
			// set
			err := scli.Client().SetDefaultContainer(args[0])
			checkCLI(err)
		}

		return nil
	},
}
