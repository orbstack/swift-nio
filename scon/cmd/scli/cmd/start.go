package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

var (
	flagAll bool
)

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Start all machines")
}

var startCmd = &cobra.Command{
	Use:   "start [flags] [ID/NAME]...",
	Short: "Start OrbStack or a machine",
	Long: `Start the specified machine(s), by ID or name.

If no machines are specified, the command will start all machines that were running when it was last stopped.
`,
	Example: "  " + appid.ShortCmd + " start ubuntu",
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
				// start VM instead
				if vmclient.IsRunning() {
					cmd.PrintErrln("OrbStack is already running. Docker engine is ready to use.\nUse --all to start all machines.")
					return nil
				}

				spinner := spinutil.Start("green", "Starting machines")
				err := vmclient.EnsureSconVM()
				spinner.Stop()
				checkCLI(err)
			}

			containerNames = args
		}

		for _, containerName := range containerNames {
			// k8s special case: enable config and (re)start docker machine
			if containerName == types.ContainerNameK8s {
				// enable config
				config, err := vmclient.Client().GetConfig()
				checkCLI(err)
				config.K8sEnable = true
				err = vmclient.Client().SetConfig(config)
				checkCLI(err)

				c, err := scli.Client().GetByID(types.ContainerIDDocker)
				checkCLI(err)

				spinner := spinutil.Start("green", "Starting k8s")
				if c.State == types.ContainerStateRunning {
					err = scli.Client().ContainerRestart(c)
				} else {
					err = scli.Client().ContainerStart(c)
				}
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
			spinner := spinutil.Start("green", "Starting "+c.Name)
			err = scli.Client().ContainerStart(c)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
