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
	dockerVolumeCmd.AddCommand(dockerVolumeExportCmd)
}

var dockerVolumeExportCmd = &cobra.Command{
	Use:   "export [VOLUME_NAME] [OUTPUT_PATH]",
	Short: "Export a volume to a .tar.zst archive",
	Long: `Export a volume to a .tar.zst archive.

The archive can be imported with the "` + rootCmd.Use + ` docker volume import" command.
`,
	Example:           "  " + rootCmd.Use + " docker volume export example example.tar.zst",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completions.TwoArgs(completions.DockerVolumes, completions.FileExtensionTarZst),
	RunE: func(cmd *cobra.Command, args []string) error {
		volumeName := args[0]
		outputPath := args[1]

		// resolve to absolute path if it's relative
		absOutputPath, err := filepath.Abs(outputPath)
		if err != nil {
			checkCLI(fmt.Errorf("failed to resolve input path: %w", err))
		}

		scli.EnsureSconVMWithSpinner()

		// spinner
		spinner := spinutil.Start("blue", "Exporting '"+volumeName+"'")
		err = scli.Client().InternalDockerExportVolumeToHostPath(types.InternalDockerExportVolumeToHostPathRequest{
			VolumeID: volumeName,
			HostPath: absOutputPath,
		})
		spinner.Stop()
		checkCLI(err)

		return nil
	},
}
