package cmd

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func init() {
	rootCmd.AddCommand(logoutCmd)
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of your OrbStack account",
	Long: `Log out of your OrbStack account, if logged in.
`,
	Example: "  " + appid.ShortCmd + " logout",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// shell out to vmgr, like login
		vmgrExe, err := vmclient.FindVmgrExe()
		checkCLI(err)

		// multi-threaded exec is safe: it terminates other threads
		err = unix.Exec(vmgrExe, []string{vmgrExe, "_logout"}, os.Environ())
		checkCLI(err)

		return nil
	},
}
