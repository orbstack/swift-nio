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
	rootCmd.AddCommand(stopCmd)
}

var stopCmd = &cobra.Command{
	Use:   "stop [ID/NAME]",
	Short: "Stop a Linux container",
	Long: `Stop the specified Linux container, by ID or name.
`,
	Example: "  " + appid.ShortCtl + " stop ubuntu",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		check(err)

		if !c.Running {
			cmd.PrintErrln("Container is not running")
			return nil
		}

		// spinner
		isPty := term.IsTerminal(int(os.Stdout.Fd()))
		var spin *spinner.Spinner
		if isPty {
			spin = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
			spin.Color("red")
			spin.Suffix = " Stopping " + c.Name
			spin.Start()
		}

		err = scli.Client().ContainerStop(c)
		if isPty {
			spin.Stop()
		}
		check(err)

		return nil
	},
}
