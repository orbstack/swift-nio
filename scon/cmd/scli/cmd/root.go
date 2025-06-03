package cmd

import (
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

const groupMachines = "machines"
const groupContainers = "containers"
const groupGeneral = "general"

func init() {
	rootCmd.AddGroup(&cobra.Group{
		ID:    groupMachines,
		Title: "Linux machines:",
	}, &cobra.Group{
		ID:    groupContainers,
		Title: "Containers:",
	}, &cobra.Group{
		ID:    groupGeneral,
		Title: "General:",
	})
}

func use() string {
	if filepath.Base(os.Args[0]) == appid.ShortCmd {
		return appid.ShortCmd
	}

	return appid.ShortCtl
}

var rootCmd = &cobra.Command{
	Use:   use(),
	Short: "Linux integration utilities for " + appid.UserAppName,
	Long: `Use and manage ` + appid.UserAppName + ` and its machines.

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
