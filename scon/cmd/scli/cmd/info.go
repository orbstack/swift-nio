package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/completions"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(infoCmd)
	infoCmd.Flags().StringVarP(&flagFormat, "format", "f", "", "output format (text, json)")

	infoCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return []cobra.Completion{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
}

var infoCmd = &cobra.Command{
	GroupID: groupMachines,
	Use:     "info [ID/NAME]",
	Aliases: []string{"i"},
	Short:   "Get info about a machine",
	Long: `Get info about the specified machine, by ID or name.
`,
	Example:           "  " + rootCmd.Use + " info ubuntu",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completions.Limit(1, completions.Machines),
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

			if c.DiskUsage != nil {
				fmt.Printf("\n")
				fmt.Printf("Disk usage: %s\n", cliutil.ByteCountSI(int64(*c.DiskUsage)))
			}

			if c.IP4 != nil {
				fmt.Printf("\n")
				fmt.Printf("IPv4: %s\n", c.IP4)
			}
			if c.IP6 != nil {
				fmt.Printf("IPv6: %s\n", c.IP6)
			}

			if c.Record.Builtin {
				fmt.Printf("\nMachine is built-in.")
			}

			fmt.Printf("\n")
		}

		return nil
	},
}
