package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "macctl",
	Short: "macOS integration utilities for " + appid.UserAppName,
	Long:  `Control and interact with macOS from ` + appid.UserAppName + ` Linux distros.`,
}

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

func HasCommand(args []string) bool {
	_, _, err := rootCmd.Find(args)
	return err == nil
}
