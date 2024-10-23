package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "show all logs (useful for debugging)")
}

var logsCmd = &cobra.Command{
	Use:     "logs [ID/NAME]",
	Aliases: []string{"log", "console"},
	Short:   "Show logs for a machine",
	Long: `Show the unified logs for the specified machine, by ID or name.
`,
	Example: "  " + rootCmd.Use + " logs ubuntu",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		// k8s special case: same as docker machine
		if args[0] == types.ContainerNameK8s {
			args[0] = types.ContainerIDDocker
		}

		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		checkCLI(err)

		red := color.New(color.FgRed)
		header := color.New(color.Bold)
		if flagAll {
			header.Println("Runtime:")
			logRuntime, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
			if err != nil {
				red.Println(err)
			} else {
				fmt.Println(logRuntime)
			}

			header.Println("\n\n\nConsole:")
		}

		logConsole, err := scli.Client().ContainerGetLogs(c, types.LogConsole)
		if err != nil {
			red.Println(err)
		} else {
			fmt.Println(logConsole)
		}

		return nil
	},
}
