package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/conf/appver"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show OrbStack version",
	Long: `Show OrbStack version information.
`,
	Example: "  " + appid.ShortCtl + " version",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ver := appver.Get()
		fmt.Printf("Version: %s (%d)\n", ver.Short, ver.Code)
		fmt.Printf("Commit: %s (%s)\n", ver.GitCommit, ver.GitDescribe)

		return nil
	},
}
