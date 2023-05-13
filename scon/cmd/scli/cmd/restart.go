package cmd

import (
	"errors"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/vmclient"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(restartCmd)
	restartCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Restart all machines")
}

var restartCmd = &cobra.Command{
	Use:   "restart [flags] [ID/NAME]..",
	Short: "Restart a Linux machine",
	Long: `Restart the specified Linux machine(s), by ID or name.
`,
	Example: "  " + appid.ShortCtl + " restart ubuntu",
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

			// spinner
			spinner := spinutil.Start("green", "Restarting "+c.Name)
			err = scli.Client().ContainerRestart(c)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
