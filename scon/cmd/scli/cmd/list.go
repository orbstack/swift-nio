package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagRunning bool
)

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVarP(&flagRunning, "running", "r", false, "only show running machines")
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all Linux machines",
	Long: `List all Linux machines and statuses.
`,
	Example: "  " + appid.ShortCtl + " list",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		containers, err := scli.Client().ListContainers()
		checkCLI(err)

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		defer w.Flush()

		if term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintf(w, "NAME\tSTATUS\tDISTRO\tVERSION\tARCH\n")
			fmt.Fprintf(w, "----\t------\t------\t-------\t----\n")
		}
		for _, c := range containers {
			if c.Builtin {
				continue
			}
			if flagRunning && !c.Running {
				continue
			}

			status := "stopped"
			if c.Running {
				status = "running"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Name, status, c.Image.Distro, c.Image.Version, c.Image.Arch)
		}

		if len(containers) == 0 {
			fmt.Fprintln(os.Stderr, `\nUse "`+appid.ShortCtl+`" create to create a machine.`)
		}

		return nil
	},
}
