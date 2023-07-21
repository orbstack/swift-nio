package cmd

import (
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   appid.ShortCmd,
	Short: "Linux integration utilities for " + appid.UserAppName,
	Long: `Use and manage ` + appid.UserAppName + ` and its Linux machines.

The listed commands can be used with either "` + appid.ShortCtl + `" or "` + appid.ShortCmd + `".

You can also prefix commands with "` + appid.ShortCmd + `" to run them on Linux. For example:
    ` + appid.ShortCmd + ` uname -a
will run "uname -a" on macOS, and is equivalent to:
    ` + appid.ShortCtl + ` run uname -a

In this mode, the default user and machine will be used.`,
}

// Execute executes the root command.
func Execute() error {
	return rootCmd.Execute()
}

func HasCommand(args []string) bool {
	// search only by first argument
	// if it's a flag (e.g. -p) we want to keep it as a flag to "run"
	targetCmd, _, err := rootCmd.Find(args[:1])
	if err != nil {
		return false
	}

	return targetCmd != rootCmd
}
