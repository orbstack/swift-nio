package cmd

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	flagDomain string
)

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force re-login if already logged in")
	authCmd.Flags().StringVarP(&flagDomain, "domain", "", "", "Domain for SSO")
}

var authCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and activate your OrbStack license",
	Long: `Log in to your OrbStack account and activate your license, if any.

If you are already logged in, this command will do nothing unless you add --force.
`,
	Example: "  " + appid.ShortCmd + " login",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// login CLI is in vmgr so we get the right keychain access group
		// otherwise we'd have to give scli its own wrapper app bundle, signing ID, and provisioning profile
		vmgrExe, err := vmclient.FindVmgrExe()
		checkCLI(err)

		forceArg := "false"
		if flagForce {
			forceArg = "true"
		}

		// multi-threaded exec is safe: it terminates other threads
		err = unix.Exec(vmgrExe, []string{vmgrExe, "_login", forceArg, flagDomain}, os.Environ())
		checkCLI(err)

		return nil
	},
}
