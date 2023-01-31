package cmd

import (
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   appid.ShortCtl,
	Short: "Linux integration utilities for " + appid.UserAppName,
	Long: `Control and interact with ` + appid.UserAppName + ` Linux distros from macOS.

The listed commands can be used with either "` + appid.ShortCtl + `" or "` + appid.ShortCmd + `".

You can also prefix commands with "` + appid.ShortCmd + `" to run them on Linux. For example:
	` + appid.ShortCmd + ` uname -a
will run "uname -a" on macOS, and is equivalent to:
	` + appid.ShortCtl + ` run uname -a

In this mode, the default user (matching your macOS username) and last-used distro will be used.`,
}

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

func HasCommand(args []string) bool {
	_, _, err := rootCmd.Find(args)
	return err == nil
}
