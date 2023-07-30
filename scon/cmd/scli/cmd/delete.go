package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Delete all machines")
	deleteCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force deletion without confirmation")
}

var deleteCmd = &cobra.Command{
	Use:   "delete [flags] [ID/NAME]...",
	Short: "Delete a Linux machine",
	Long: `Delete the specified Linux machine, by ID or name.

The machine will be stopped if it is running.
All files stored in the machine will be PERMANENTLY LOST without warning!
`,
	Example: "  " + appid.ShortCmd + " delete ubuntu",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// confirm
		if !flagForce && (flagAll || len(args) > 0) {
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

			if flagAll && c.Builtin {
				continue
			}

			// spinner
			scli.EnsureSconVMWithSpinner()
			spinner := spinutil.Start("red", "Deleting "+c.Name)
			err = scli.Client().ContainerDelete(c)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
