package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(migrateCmd)
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate data from Docker Desktop to OrbStack",
	Long:  `Migrate data from Docker Desktop to OrbStack.`,
}
