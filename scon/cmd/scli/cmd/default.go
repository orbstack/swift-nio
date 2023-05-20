package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(defaultCmd)
	defaultCmd.Flags().StringVarP(&flagUser, "user", "u", "", "Change the default login user")
}

var defaultCmd = &cobra.Command{
	Use:     "default [NAME/ID]",
	Aliases: []string{"set-default", "get-default"},
	Short:   "Get or set the default machine",
	Long: `Get or set the default machine and/or user used when running commands without specifying a machine.

You can remove the default machine by passing "none" as the machine name.
If no default is set, the most recently-used machine will be used instead.

If --user is specified, the default user will be changed.
Otherwise, it will be reset to match your macOS username.
`,
	Example: "  " + appid.ShortCtl + " set-default -u root ubuntu",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		if len(args) == 0 {
			// get
			username, err := scli.Client().GetDefaultUsername()
			checkCLI(err)

			c, err := scli.Client().GetDefaultContainer()
			checkCLI(err)
			if c == nil || c.ID == "" {
				cmd.PrintErrln("No default machine set. (will pick most recently-used machine)")
			} else {
				if username != "" {
					fmt.Printf("%s@%s\n", username, c.Name)
				} else {
					fmt.Println(c.Name)
				}
			}
		} else {
			// set
			if args[0] == "none" {
				// remove default
				err := scli.Client().SetDefaultContainer(nil)
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

		// now set or reset username, always if we have something to mutate
		// (i.e. if we either have a username or are changing container)
		if flagUser != "" || len(args) > 0 {
			err := scli.Client().SetDefaultUsername(flagUser)
			checkCLI(err)
		}

		return nil
	},
}
