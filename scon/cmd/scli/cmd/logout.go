package cmd

import (
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	GroupID: groupGeneral,
	Use:     "logout",
	Short:   "Log out of your OrbStack account",
	Long: `Log out of your OrbStack account, if logged in.
`,
	Example:           "  " + rootCmd.Use + " logout",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := drmcore.SaveRefreshToken("")
		checkCLI(err)

		// if running, update it in vmgr so it takes effect
		if vmclient.IsRunning() {
			err = vmclient.Client().InternalUpdateToken("")
			checkCLI(err)
		}

		return nil
	},
}
