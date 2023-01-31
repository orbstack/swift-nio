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
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start [ID/NAME]",
	Short: "Start a Linux container",
	Long: `Start the specified Linux container, by ID or name.
`,
	Example: "  " + appid.ShortCtl + " start ubuntu",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// try ID first
		c, err := scli.Client().GetByID(args[0])
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(args[0])
		}
		check(err)

		if c.Running {
			cmd.PrintErrln("Container is already running")
			return nil
		}

		// spinner
		isPty := term.IsTerminal(int(os.Stdout.Fd()))
		var spin *spinner.Spinner
		if isPty {
			spin = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
			spin.Color("green")
			spin.Suffix = " Starting " + c.Name
			spin.Start()
		}

		err = scli.Client().ContainerStart(c)
		if isPty {
			spin.Stop()
		}
		check(err)

		return nil
	},
}
