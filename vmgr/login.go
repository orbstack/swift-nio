package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/appapi"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vmclient"
)

var (
	errCLIPanic = errors.New("cli panic")
)

func checkCLI(err error) {
	if err != nil {
		red := color.New(color.FgRed).FprintlnFunc()
		red(os.Stderr, err)

		// may need to do cleanup, so don't exit
		panic(errCLIPanic)
	}
}

// login CLI is in vmgr so we get the right keychain access group
// otherwise we'd have to give scli its own wrapper app bundle, signing ID, and provisioning profile
func runLogin() {
	flagForce := os.Args[2] == "true"
	flagDomain := os.Args[3]

	if !flagForce && drmcore.HasRefreshToken() {
		fmt.Println("Already logged in.")
		return
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
}

func runLogout() {
	err := drmcore.SaveRefreshToken("")
	checkCLI(err)

	// if running, update it in vmgr so it takes effect
	if vmclient.IsRunning() {
		err = vmclient.Client().InternalUpdateToken("")
		checkCLI(err)
	}
}
