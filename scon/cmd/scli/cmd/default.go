package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(defaultCmd)
}

var defaultCmd = &cobra.Command{
	Use:     "default [NAME/ID]",
	Aliases: []string{"set-default", "get-default"},
	Short:   "Get or set the default machine",
	Long: `Get or set the default machine used when running commands without specifying a machine.

You can remove the default machine by passing "none" as the machine name.
If no default is set, the most recently-used machine will be used instead.
`,
	Example: "  " + appid.ShortCtl + " set-default ubuntu",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		if len(args) == 0 {
			hasDefault, err := scli.Client().HasExplicitDefaultContainer()
			checkCLI(err)
			if !hasDefault {
				cmd.PrintErrln("No default machine set.")
			}

			// get default
			c, err := scli.Client().GetDefaultContainer()
			checkCLI(err)

			if c == nil {
				cmd.PrintErrln("No machines found.")
			} else {
				if !hasDefault {
					cmd.PrintErr("Most recently used: ")
				}
				fmt.Println(c.Name)
			}
		} else {
			// set default
			if args[0] == "none" {
				// remove default
				err := scli.Client().ClearDefaultContainer()
				checkCLI(err)
			} else {
				// set default:

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
		}

		return nil
	},
}
