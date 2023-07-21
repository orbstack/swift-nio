package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/orbstack/macvirt/scon/cmd/scli/cliutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
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
	Short: "List all Linux machines",
	Long: `List all Linux machines and their statuses.
`,
	Example: "  " + appid.ShortCmd + " list",
	Args:    cobra.NoArgs,
	// no "ps" alias because of conflict with short cmd
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		containers, err := scli.Client().ListContainers()
		checkCLI(err)

		if flagQuiet {
			for _, c := range containers {
				if c.Builtin {
					continue
				}
				if flagRunning && c.State != types.ContainerStateRunning {
					continue
				}

				fmt.Println(c.Name)
			}
			return nil
		}

		if flagFormat == "json" {
			// don't print "null" for empty array
			displayContainers := []types.ContainerRecord{}
			for _, c := range containers {
				if c.Builtin {
					continue
				}
				if flagRunning && c.State != types.ContainerStateRunning {
					continue
				}

				displayContainers = append(displayContainers, c)
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
				if c.Builtin {
					continue
				}
				if flagRunning && c.State != types.ContainerStateRunning {
					continue
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.State, c.Image.Distro, c.Image.Version, c.Image.Arch)
			}

			if len(containers) == 0 {
				fmt.Fprintln(os.Stderr, `\nUse "`+appid.ShortCmd+`" create to create a machine.`)
			}
		}

		return nil
	},
}
