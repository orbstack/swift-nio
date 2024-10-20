package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/bugreport"
	"github.com/spf13/cobra"
)

type generatedReport struct {
	ZipPath string `json:"zip_path"`
	Info    string `json:"info"`
}

func init() {
	internalCmd.AddCommand(internalGenerateDiagReport)
}

var internalGenerateDiagReport = &cobra.Command{
	Use:    "generate-diag-report",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var buffer bytes.Buffer

		err := bugreport.GenerateInfo(&buffer)
		checkCLI(err)
		zipPath, _, err := bugreport.Build(buffer.Bytes())
		checkCLI(err)

		output, err := json.Marshal(generatedReport{ZipPath: zipPath, Info: string(buffer.Bytes())})
		checkCLI(err)

		fmt.Println(string(output))

		return nil
	},
}
