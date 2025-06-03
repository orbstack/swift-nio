package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(migrateCmd)
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate data to or from OrbStack",
	Long:  `Migrate data to or from OrbStack.`,

	// deprecated alias
	Hidden: true,
}
