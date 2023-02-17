package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/drm/updates"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update OrbStack",
	Long: `Update OrbStack.

If an update is available, it will be downloaded and installed,
and your machines and containers will be restarted to complete the update.

If there are no updates, this command will do nothing.

Updating is important to get the latest improvements, features, and security fixes.
This includes the Linux kernel, Docker, the CLI, GUI app, and other components.
`,
	Example: "  " + appid.ShortCtl + " update",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		// stop first
		if vmclient.IsRunning() {
			// spinner
			spinner := spinutil.Start("red", "Stopping VM and machines")
			var err error
			if flagForce {
				err = vmclient.Client().ForceStop()
			} else {
				err = vmclient.Client().Stop()
			}
			spinner.Stop()
			checkCLI(err)
		}

		// grab our called exe path before updating
		exe, err := os.Executable()
		checkCLI(err)
		fmt.Println("got exe", exe)

		fmt.Println("checking for updates")
		cmd, err := updates.NewSparkleCommand("--check-immediately", "--verbose", "--interactive")
		checkCLI(err)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		fmt.Println("sparkle run err", err)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				checkCLI(err)
			}
		}

		// respawn - this triggers update/start check and nothing else
		fmt.Println("respawning", exe)
		cmd = exec.Command(exe, "ping")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		fmt.Println("respawn err", err)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				checkCLI(err)
			}
		}

		fmt.Println("update complete")
		return nil
	},
}
