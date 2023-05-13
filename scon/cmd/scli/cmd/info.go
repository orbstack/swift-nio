package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(infoCmd)
}

var infoCmd = &cobra.Command{
	Use:     "info [ID/NAME]",
	Aliases: []string{"i"},
	Short:   "Get information about a Linux machine",
	Long: `Get information about the specified Linux machine, by ID or name.
`,
	Example: "  " + appid.ShortCtl + " list",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		checkCLI(err)

		fmt.Printf("ID: %s\n", c.ID)
		fmt.Printf("Name: %s\n", c.Name)
		fmt.Printf("State: %s\n", c.State)
		fmt.Printf("\n")
		fmt.Printf("Distro: %s\n", c.Image.Distro)
		fmt.Printf("Version: %s\n", c.Image.Version)
		fmt.Printf("Architecture: %s\n", c.Image.Arch)

		if c.Builtin {
			fmt.Printf("\nMachine is built-in.\n")
		}

		return nil
	},
}
