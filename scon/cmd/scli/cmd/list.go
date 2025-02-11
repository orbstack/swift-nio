package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagRunning bool
	flagQuiet   bool
	flagFormat  string
)

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVarP(&flagRunning, "running", "r", false, "only show running machines")
	listCmd.Flags().BoolVarP(&flagQuiet, "quiet", "q", false, "only show machine names")
	listCmd.Flags().StringVarP(&flagFormat, "format", "f", "", "output format (json)")
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List machines",
	Long: `List machines and their respective status.
`,
	Example: "  " + rootCmd.Use + " list",
	Args:    cobra.NoArgs,
	// no "ps" alias because of conflict with short cmd
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		containers, err := scli.Client().ListContainers()
		checkCLI(err)

		if flagQuiet {
			for _, c := range containers {
				if c.Record.Builtin {
					continue
				}
				if flagRunning && c.Record.State != types.ContainerStateRunning {
					continue
				}

				fmt.Println(c.Record.Name)
			}
			return nil
		}

		if flagFormat == "json" {
			// don't print "null" for empty array
			displayContainers := []*types.ContainerRecord{}
			for _, c := range containers {
				if c.Record.Builtin {
					continue
				}
				if flagRunning && c.Record.State != types.ContainerStateRunning {
					continue
				}

				displayContainers = append(displayContainers, c.Record)
			}

			cliutil.PrintJSON(displayContainers)
		} else {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer w.Flush()

			if term.IsTerminal(int(os.Stdout.Fd())) {
				fmt.Fprintf(w, "NAME\tSTATE\tDISTRO\tVERSION\tARCH\n")
				fmt.Fprintf(w, "----\t-----\t------\t-------\t----\n")
			}
			for _, c := range containers {
				if c.Record.Builtin {
					continue
				}
				if flagRunning && c.Record.State != types.ContainerStateRunning {
					continue
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Record.Name, c.Record.State, c.Record.Image.Distro, c.Record.Image.Version, c.Record.Image.Arch)
			}

			if len(containers) == 0 {
				fmt.Fprintln(os.Stderr, `\nUse "`+rootCmd.Use+`" create to create a machine.`)
			}
		}

		return nil
	},
}
