package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
	deleteCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Stop all machines")
	deleteCmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt (for --all)")
}

var deleteCmd = &cobra.Command{
	Use:   "delete [flags] [ID/NAME]...",
	Short: "Delete a Linux machine",
	Long: `Delete the specified Linux machine, by ID or name.

The machine will be stopped if it is running.
All files stored in the machine will be PERMANENTLY LOST without warning!
`,
	Example: "  " + appid.ShortCtl + " delete ubuntu",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// confirm
		if flagAll && !flagYes {
			cmd.PrintErrln("WARNING: This will PERMANENTLY DELETE ALL DATA in ALL of your machines!")
			cmd.PrintErrln("This includes all Linux data.")
			cmd.PrintErrln("This cannot be undone.")
			cmd.PrintErrln("")
			cmd.PrintErr("Are you sure you want to continue [y/N]? ")
			var resp string
			_, err := fmt.Scanln(&resp)
			checkCLI(err)
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
