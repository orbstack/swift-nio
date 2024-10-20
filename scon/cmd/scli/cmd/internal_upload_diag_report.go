package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/bugreport"
	"github.com/spf13/cobra"
)

func init() {
	internalCmd.AddCommand(internalUploadDiagReport)
}

var internalUploadDiagReport = &cobra.Command{
	Use:    "upload-diag-report",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := bugreport.FromZip(args[0])
		checkCLI(err)
		downloadURL, err := report.Upload()
		checkCLI(err)

		fmt.Println(downloadURL)

		return nil
	},
}
