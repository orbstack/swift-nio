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

func init() {
	dockerVolumeCmd.AddCommand(dockerVolumeImportCmd)
	dockerVolumeImportCmd.Flags().StringVarP(&flagName, "name", "n", "", "Name of the new volume (default: same as original volume)")

	dockerVolumeImportCmd.RegisterFlagCompletionFunc("name", completions.DockerVolumes)
}

var dockerVolumeImportCmd = &cobra.Command{
	Use:   "import [INPUT_PATH]",
	Short: "Import a volume from an exported .tar.zst archive",
	Long: `Import a volume from an exported .tar.zst archive.

The archive must have been created with the "` + rootCmd.Use + ` docker volume export" command.
`,
	Example:           "  " + rootCmd.Use + " docker volume import my-volume.tar.zst",
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
		err = scli.Client().InternalDockerImportVolumeFromHostPath(types.InternalDockerImportVolumeFromHostPathRequest{
			NewName:  flagName,
			HostPath: absInputPath,
		})
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
