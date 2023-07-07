package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Stop all machines")
	stopCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force VM to stop immediately (if no arguments). May cause data loss!")
}

var stopCmd = &cobra.Command{
	Use:   "stop [flags] [ID/NAME]..",
	Short: "Stop a Linux machine",
	Long: `Stop the specified Linux machine(s), by ID or name.

If no arguments are provided, this command will stop the entire OrbStack service, including Docker and all machines.
`,
	Example: "  " + appid.ShortCtl + " stop ubuntu",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !vmclient.IsRunning() {
			cmd.PrintErrln("OrbStack is not running")
			return nil
		}

		var containerNames []string
		if flagAll {
			containers, err := scli.Client().ListContainers()
			checkCLI(err)

			for _, c := range containers {
				containerNames = append(containerNames, c.Name)
			}
		} else {
			if len(args) == 0 {
				// no args = stop VM (shutdown)
				spinner := spinutil.Start("red", "Stopping Docker and machines")
				var err error
				if flagForce {
					err = vmclient.Client().SyntheticForceStopOrKill()
				} else {
					err = vmclient.Client().SyntheticStopOrKill()
				}
				spinner.Stop()
				checkCLI(err)
				return nil
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

			// spinner
			spinner := spinutil.Start("red", "Stopping "+c.Name)
			err = scli.Client().ContainerStop(c)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
