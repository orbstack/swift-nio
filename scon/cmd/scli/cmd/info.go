package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(infoCmd)
	infoCmd.Flags().StringVarP(&flagFormat, "format", "f", "", "output format (json)")
}

var infoCmd = &cobra.Command{
	Use:     "info [ID/NAME]",
	Aliases: []string{"i"},
	Short:   "Get info about a machine",
	Long: `Get info about the specified machine, by ID or name.
`,
	Example: "  " + rootCmd.Use + " list",
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

		if flagFormat == "json" {
			cliutil.PrintJSON(c)
		} else {
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
		}

		return nil
	},
}
