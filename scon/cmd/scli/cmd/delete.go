package cmd

import (
	"os"
	"time"

	"github.com/briandowns/spinner"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(deleteCmd)
}

var deleteCmd = &cobra.Command{
	Use:   "delete [ID/NAME]",
	Short: "Delete a Linux container",
	Long: `Delete the specified Linux container, by ID or name.

The container will be stopped if it is running.
All files stored in the container will be PERMANENTLY LOST without warning!
`,
	Example: "  " + appid.ShortCtl + " delete ubuntu",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		check(err)

		// spinner
		isPty := term.IsTerminal(int(os.Stdout.Fd()))
		var spin *spinner.Spinner
		if isPty {
			spin = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
			spin.Color("red")
			spin.Suffix = " Deleting " + c.Name
			spin.Start()
		}

		err = scli.Client().ContainerDelete(c)
		if isPty {
			spin.Stop()
		}
		check(err)

		return nil
	},
}
