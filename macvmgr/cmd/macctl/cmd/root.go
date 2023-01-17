package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "macctl",
	Short: "macOS integration utilities for " + conf.AppNameUser(),
	Long:  `Control and interact with macOS from ` + conf.AppNameUser() + ` Linux distros.`,
}

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}
