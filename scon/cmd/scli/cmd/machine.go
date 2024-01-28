package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(machineCmd)
}

var machineCmd = &cobra.Command{
	Use:   "machine",
	Short: "Manage OrbStack machines",
	Long:  `Manage OrbStack machines.`,
}
