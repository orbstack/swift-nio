package cmd

import (
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of your OrbStack account",
	Long: `Log out of your OrbStack account, if logged in.
`,
	Example: "  " + appid.ShortCmd + " login",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		vmgrExe, err := vmclient.FindVmgrExe()
		checkCLI(err)
		err = util.Run(vmgrExe, "set-refresh-token", "")
		checkCLI(err)

		// if running, update it in vmgr so it takes effect
		if vmclient.IsRunning() {
			err = vmclient.Client().InternalUpdateToken("")
			checkCLI(err)
		}

		return nil
	},
}
