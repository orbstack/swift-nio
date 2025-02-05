package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(exportCmd)
}

var exportCmd = &cobra.Command{
	Use:   "export [MACHINE_NAME] [OUTPUT_PATH]",
	Short: "Export a machine to a file",
	Long: `Export a machine to a file.

To prevent data corruption, the existing machine will be paused while exporting.
`,
	Example: `  orb export ubuntu foo.tar.zst`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		oldNameOrID := args[0]
		outputPath := args[1]

		// resolve to absolute path if it's relative
		outputPath, err := filepath.Abs(outputPath)
		if err != nil {
			checkCLI(fmt.Errorf("failed to resolve output path: %w", err))
		}

		scli.EnsureSconVMWithSpinner()

		// get old container
		c, err := scli.Client().GetByID(oldNameOrID)
		if err != nil {
			// try name
			c, err = scli.Client().GetByName(oldNameOrID)
		}
		checkCLI(err)

		// spinner
		spinner := spinutil.Start("blue", "Exporting "+oldNameOrID)
		err = scli.Client().ContainerExportToHostPath(c.Record, outputPath)
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
