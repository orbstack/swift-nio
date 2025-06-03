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
	GroupID: groupMachines,
	Use:     "info [ID/NAME]",
	Aliases: []string{"i"},
	Short:   "Get info about a machine",
	Long: `Get info about the specified machine, by ID or name.
`,
	Example: "  " + rootCmd.Use + " list",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		c, err := scli.Client().GetByKey(args[0])
		checkCLI(err)

		if flagFormat == "json" {
			cliutil.PrintJSON(c)
		} else {
			fmt.Printf("ID: %s\n", c.Record.ID)
			fmt.Printf("Name: %s\n", c.Record.Name)
			fmt.Printf("State: %s\n", c.Record.State)
			fmt.Printf("\n")
			fmt.Printf("Distro: %s\n", c.Record.Image.Distro)
			fmt.Printf("Version: %s\n", c.Record.Image.Version)
			fmt.Printf("Architecture: %s\n", c.Record.Image.Arch)

			if c.Record.Builtin {
				fmt.Printf("\nMachine is built-in.\n")
			}
		}

		return nil
	},
}
