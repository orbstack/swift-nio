package cmd

import (
	"errors"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(restartCmd)
	restartCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Restart all machines")
}

var restartCmd = &cobra.Command{
	Use:   "restart [flags] [ID/NAME]..",
	Short: "Restart a machine",
	Long: `Restart the specified machine(s), by ID or name.
`,
	Example: "  " + appid.ShortCmd + " restart ubuntu",
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
			// k8s special case: enable config and restart docker machine
			if containerName == types.ContainerNameK8s {
				// enable config
				config, err := vmclient.Client().GetConfig()
				checkCLI(err)
				config.K8sEnable = true
				err = vmclient.Client().SetConfig(config)
				checkCLI(err)

				c, err := scli.Client().GetByID(types.ContainerIDDocker)
				checkCLI(err)

				spinner := spinutil.Start("green", "Restarting k8s")
				err = scli.Client().ContainerRestart(c)
				spinner.Stop()
				checkCLI(err)

				continue
			}

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
