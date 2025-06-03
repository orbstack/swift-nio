package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	dockerCmd.AddCommand(dockerVolumeCmd)
}

var dockerVolumeCmd = &cobra.Command{
	Use:   "volume",
	Short: "Extension commands for volumes",
	Long: `OrbStack's extension commands for managing Docker volumes.
`,
}
