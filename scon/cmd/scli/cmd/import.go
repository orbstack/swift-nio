package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/orbstack/macvirt/scon/cmd/scli/completions"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/spf13/cobra"
)

var (
	flagName string
)

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().StringVarP(&flagName, "name", "n", "", "Name of the machine")

	importCmd.RegisterFlagCompletionFunc("name", completions.Machines)
}

var importCmd = &cobra.Command{
	GroupID: groupMachines,
	Use:     "import [INPUT_PATH]",
	Short:   "Import a machine from a file",
	Long: `Import a machine from a file.

To prevent data corruption, the existing machine will be paused while exporting.
`,
	Example:           `  orb import -n ubuntu foo.tar.zst`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completions.Limit(1, completions.FileExtensionTarZst),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputPath := args[0]

		// resolve to absolute path if it's relative
		absInputPath, err := filepath.Abs(inputPath)
		if err != nil {
			checkCLI(fmt.Errorf("failed to resolve input path: %w", err))
		}

		scli.EnsureSconVMWithSpinner()

		// spinner
		spinner := spinutil.Start("blue", "Importing "+inputPath)
		_, err = scli.Client().ImportContainerFromHostPath(types.ImportContainerFromHostPathRequest{
			NewName:  flagName,
			HostPath: absInputPath,
		})
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
