package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(defaultCmd)
}

var defaultCmd = &cobra.Command{
	Use:     "default [NAME/ID]",
	Aliases: []string{"set-default", "get-default"},
	Short:   "Get or set the default machine",
	Long: `Get or set the default machine used for commands.

Use "orb config" to change the default username.
`,
	Example: "  " + appid.ShortCmd + " set-default ubuntu",
	Args:    cobra.MaximumNArgs(1),
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
			// try ID first
			c, err := scli.Client().GetByID(args[0])
			if err != nil {
				// try name
				c, err = scli.Client().GetByName(args[0])
			}
			checkCLI(err)

			err = scli.Client().SetDefaultContainer(c)
			checkCLI(err)
		}

		return nil
	},
}
