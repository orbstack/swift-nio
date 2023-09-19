package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/appapi"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(authCmd)
}

var authCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and activate your OrbStack license",
	Long: `Log in to your OrbStack account and activate your license, if any.
`,
	Example: "  " + appid.ShortCmd + " login",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO check if already logged in

		client := appapi.NewClient()

		// generate a token
		var startResp drmtypes.StartAppAuthResponse
		err := client.Post("/app/start_auth", nil, &startResp)
		checkCLI(err)

		// print
		cmd.Println("Finish logging in at: " + startResp.AuthURL)

		// open url in browser
		err = util.Run("open", startResp.AuthURL)
		checkCLI(err)

		// wait
		var waitResp drmtypes.WaitAppAuthResponse
		spinner := spinutil.Start("blue", "Waiting for login...")
		err = client.LongGet("/app/wait_auth?id="+startResp.SessionID, &waitResp)
		spinner.Stop()
		checkCLI(err)

		// save token
		// err = drmcore.SaveRefreshToken(waitResp.RefreshToken)
		// checkCLI(err)
		//TODO
		vmgrExe, err := vmclient.FindVmgrExe()
		checkCLI(err)
		err = util.Run(vmgrExe, "set-refresh-token", waitResp.RefreshToken)
		checkCLI(err)

		// if running, update it in vmgr so it takes effect
		if vmclient.IsRunning() {
			err = vmclient.Client().InternalUpdateToken(waitResp.RefreshToken)
			checkCLI(err)
		}

		return nil
	},
}
