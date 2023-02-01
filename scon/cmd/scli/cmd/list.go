package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all Linux machines",
	Long: `List all Linux machines and statuses.
`,
	Example: "  " + appid.ShortCtl + " list",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		containers, err := scli.Client().ListContainers()
		checkCLI(err)

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		defer w.Flush()

		fmt.Fprintf(w, "NAME\tSTATUS\tDISTRO\tVERSION\tARCH\n")
		fmt.Fprintf(w, "----\t------\t------\t-------\t----\n")
		for _, c := range containers {
			status := "stopped"
			if c.Running {
				status = "running"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Name, status, c.Image.Distro, c.Image.Version, c.Image.Arch)
		}

		return nil
	},
}
