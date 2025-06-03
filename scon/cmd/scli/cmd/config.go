package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	GroupID: groupGeneral,
	Use:     "config",
	Short:   "Change OrbStack settings",
	Long:    `Change OrbStack settings.`,
}
