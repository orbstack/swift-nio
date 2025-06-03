package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func init() {
	migrateDockerCmd := *dockerMigrateCmd
	migrateDockerCmd.Use = "docker"
	migrateDockerCmd.Short = "[DEPRECATED] Use `" + rootCmd.Use + " docker migrate` instead"
	migrateDockerCmd.Long = `[DEPRECATED] Use ` + rootCmd.Use + ` docker migrate instead.
`
	migrateDockerCmd.Args = cobra.NoArgs
	migrateDockerCmd.RunE = func(cmd *cobra.Command, args []string) error {
		logrus.Warn("`" + rootCmd.Use + " migrate docker` is deprecated. Use `" + rootCmd.Use + " docker migrate` instead.")
		return dockerMigrateCmd.RunE(cmd, args)
	}

	migrateDockerCmd.Flags().AddFlagSet(dockerMigrateCmd.Flags())
	migrateCmd.AddCommand(&migrateDockerCmd)
}
