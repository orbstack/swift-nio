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
	listCmd.Flags().StringVarP(&flagFormat, "format", "f", "text", "output format (text, json)")

	listCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
}

var listCmd = &cobra.Command{
	GroupID: groupMachines,
	Use:     "list",
	Short:   "List machines",
	Long: `List machines and their respective status.
`,
	Example:           "  " + rootCmd.Use + " list",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
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
		} else if flagFormat == "text" {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			defer w.Flush()

			if term.IsTerminal(int(os.Stdout.Fd())) {
				fmt.Fprintf(w, "NAME\tSTATE\tDISTRO\tVERSION\tARCH\tSIZE\tIP\n")
				fmt.Fprintf(w, "----\t-----\t------\t-------\t----\t----\t--\n")
			}
			for _, c := range containers {
				if c.Record.Builtin {
					continue
				}
				if flagRunning && c.Record.State != types.ContainerStateRunning {
					continue
				}

				formattedDiskSize := ""
				if c.DiskUsage != nil {
					formattedDiskSize = cliutil.ByteCountSI(int64(*c.DiskUsage))
				}

				ip4 := ""
				if c.IP4 != nil {
					ip4 = c.IP4.String()
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.Record.Name, c.Record.State, c.Record.Image.Distro, c.Record.Image.Version, c.Record.Image.Arch, formattedDiskSize, ip4)
			}

			if len(containers) == 0 {
				fmt.Fprintln(os.Stderr, `\nUse "`+rootCmd.Use+`" create to create a machine.`)
			}
		} else {
			return fmt.Errorf("invalid format: %s", flagFormat)
		}

		return nil
	},
}
