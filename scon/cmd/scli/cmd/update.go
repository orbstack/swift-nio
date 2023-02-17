package cmd

import (
	"os"
	"os/exec"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/drm/updates"
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
		// grab our called exe path before updating
		exe, err := os.Executable()
		checkCLI(err)

		cmd, err := updates.NewSparkleCommand("--check-immediately", "--verbose", "--interactive")
		checkCLI(err)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				checkCLI(err)
			}
		}

		// respawn - this triggers update/start check and nothing else
		cmd = exec.Command(exe, "ping")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				checkCLI(err)
			}
		}

		return nil
	},
}
