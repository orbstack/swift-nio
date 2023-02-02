package cmd

import (
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
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
	Short: "Start Linux machines",
	Long: `Start the specified Linux machine(s), by ID or name.

If no machines are specified, the command will start all machines that were running when it was last stopped.
`,
	Example: "  " + appid.ShortCtl + " start ubuntu",
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
					cmd.PrintErrln("Some machines are already running. Use --all to start all machines.")
					return nil
				}

				spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
				spin.Color("green")
				spin.Suffix = " Starting machines"
				spin.Start()
				err := vmclient.EnsureSconVM()
				spin.Stop()
				checkCLI(err)
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

			if c.Running {
				cmd.PrintErrln(containerName + ": already running")
				continue
			}

			// spinner
			spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
			spin.Color("green")
			spin.Suffix = " Starting " + c.Name
			spin.Start()

			err = scli.Client().ContainerStart(c)
			spin.Stop()
			checkCLI(err)
		}

		return nil
	},
}
