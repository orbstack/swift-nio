package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Delete all machines")
	deleteCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force deletion without confirmation")
}

var deleteCmd = &cobra.Command{
	Use:   "delete [flags] [ID/NAME]...",
	Short: "Delete a machine",
	Long: `Delete the specified machine, by ID or name.

The machine will be stopped if it is running.
All files stored in the machine will be PERMANENTLY LOST without warning!
`,
	Example: "  " + appid.ShortCmd + " delete ubuntu",
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

		recordsForNames := make(map[string]*types.ContainerRecord)
		for _, name := range containerNames {
			// k8s special case
			if name == types.ContainerNameK8s {
				continue
			}

			// try ID first
			c, err := scli.Client().GetByID(name)
			if err != nil {
				// try name
				c, err = scli.Client().GetByName(name)
			}
			checkCLI(err)

			recordsForNames[c.Name] = c
		}

		// confirm if tty
		if !flagForce && (flagAll || len(args) > 0) && term.IsTerminal(int(os.Stdin.Fd())) {
			red := color.New(color.FgRed)
			if flagAll {
				red.Fprintln(os.Stderr, "WARNING: This will PERMANENTLY DELETE ALL DATA in ALL machines!")
			} else {

				red.Fprintln(os.Stderr, "WARNING: This will PERMANENTLY DELETE ALL DATA in the following machines:")
				for _, name := range args {
					red.Fprintln(os.Stderr, "  "+name)
				}
			}
			red.Fprintln(os.Stderr, "You cannot undo this action.")
			cmd.PrintErrln("")
			cmd.PrintErr("Continue [y/N]? ")
			var resp string
			_, err := fmt.Scanln(&resp)
			if err != nil {
				cmd.PrintErrln("Aborted")
				os.Exit(1)
			}
			lower := strings.ToLower(resp)
			if lower != "y" && lower != "yes" {
				cmd.PrintErrln("Aborted")
				os.Exit(1)
			}
		}

		for _, containerName := range containerNames {
			// k8s special case
			if containerName == types.ContainerNameK8s {
				c, err := scli.Client().GetByID(types.ContainerIDDocker)
				checkCLI(err)

				if flagAll && c.Builtin {
					continue
				}

				scli.EnsureSconVMWithSpinner()

				// disable config
				config, err := vmclient.Client().GetConfig()
				checkCLI(err)
				config.K8sEnable = false
				err = vmclient.Client().SetConfig(config)
				checkCLI(err)

				spinner := spinutil.Start("red", "Deleting k8s")
				err = scli.Client().InternalDeleteK8s()
				if err != nil {
					spinner.Stop()
					checkCLI(err)
				}

				// restart if docker was running
				if c.State == types.ContainerStateRunning {
					err = scli.Client().ContainerStart(c)
					if err != nil {
						spinner.Stop()
						checkCLI(err)
					}
				}

				spinner.Stop()

				continue
			}

			c := recordsForNames[containerName]
			if flagAll && c.Builtin {
				continue
			}

			// spinner
			scli.EnsureSconVMWithSpinner()
			spinner := spinutil.Start("red", "Deleting "+c.Name)
			err := scli.Client().ContainerDelete(c)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
