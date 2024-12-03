package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/bugreport"
	"github.com/spf13/cobra"
)

func init() {
	internalCmd.AddCommand(internalExtractDiagReport)
}

var internalExtractDiagReport = &cobra.Command{
	Use:    "extract-diag-report",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := bugreport.Extract(args[0])
		checkCLI(err)

		return nil
	},
}
