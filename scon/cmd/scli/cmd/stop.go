package cmd

import (
	"errors"
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Stop all machines")
}

var stopCmd = &cobra.Command{
	Use:   "stop [flags] [ID/NAME]..",
	Short: "Stop a Linux machine",
	Long: `Stop the specified Linux machine(s), by ID or name.
`,
	Example: "  " + appid.ShortCtl + " stop ubuntu",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var containerNames []string
		if flagAll {
			containers, err := scli.Client().ListContainers()
			checkCLI(err)

			for _, c := range containers {
				containerNames = append(containerNames, c.Name)
			}
		} else {
			if len(args) == 0 {
				return errors.New("no machines specified")
			}

			containerNames = args
		}

		for _, containerName := range containerNames {
			// try ID first
			c, err := scli.Client().GetByID(containerName)
			if err != nil {
				// try name
				c, err = scli.Client().GetByName(containerName)
			}
			checkCLI(err)

			if !c.Running {
				cmd.PrintErrln(containerName + ": not running")
				continue
			}

			// spinner
			spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
			spin.Color("red")
			spin.Suffix = " Stopping " + c.Name
			spin.Start()

			err = scli.Client().ContainerStop(c)
			spin.Stop()
			checkCLI(err)
		}

		return nil
	},
}
