package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/appapi"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/util"

	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

var (
	flagDomain string
)

func init() {
	rootCmd.AddCommand(authCmd)
	authCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force re-login if already logged in")
	authCmd.Flags().StringVarP(&flagDomain, "domain", "", "", "Domain for SSO")

	authCmd.RegisterFlagCompletionFunc("domain", cobra.NoFileCompletions)
}

var authCmd = &cobra.Command{
	GroupID: groupGeneral,
	Use:     "login",
	Short:   "Log in and activate your OrbStack license",
	Long: `Log in to your OrbStack account and activate your license, if any.

If you are already logged in, this command will do nothing unless you add --force.
`,
	Example:           "  " + rootCmd.Use + " login",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !flagForce && drmcore.HasRefreshToken() {
			fmt.Println("Already logged in.")
			return nil
		}

		client := appapi.NewClient()

		// generate a token
		var startResp drmtypes.StartAppAuthResponse
		err := client.Post("/app/start_auth", drmtypes.StartAppAuthRequest{
			SsoDomain: flagDomain,
		}, &startResp)
		checkCLI(err)

		// print
		fmt.Println("Finish logging in at: " + startResp.AuthURL)

		// open url in browser
		_, err = util.Run("open", startResp.AuthURL)
		checkCLI(err)

		// wait
		var waitResp drmtypes.WaitAppAuthResponse
		spinner := spinutil.Start("blue", "Waiting for login...")
		err = client.LongGet("/app/wait_auth?id="+startResp.SessionID, &waitResp)
		spinner.Stop()
		checkCLI(err)

		// save token
		err = drmcore.SaveRefreshToken(waitResp.RefreshToken)
		checkCLI(err)

		// if running, update it in vmgr so it takes effect
		if vmclient.IsRunning() {
			err = vmclient.Client().InternalUpdateToken(waitResp.RefreshToken)
			checkCLI(err)
		}

		return nil
	},
}
