package cmd

import (
	"fmt"

	"github.com/fatih/color"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "show all logs (useful for debugging)")
}

var logsCmd = &cobra.Command{
	Use:     "logs [ID/NAME]",
	Aliases: []string{"log", "console"},
	Short:   "Show logs for a Linux machine",
	Long: `Show the unified logs for the specified Linux machine, by ID or name.
`,
	Example: "  " + appid.ShortCtl + " logs ubuntu",
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

		red := color.New(color.FgRed)
		if flagAll {
			fmt.Println("Runtime:")
			logRuntime, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
			if err != nil {
				red.Println(err)
			} else {
				fmt.Println(logRuntime)
			}

			fmt.Println("\n\n\nConsole:")
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
