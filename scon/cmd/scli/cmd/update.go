package cmd

import (
	"os"
	"os/exec"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/drm/updates"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update OrbStack",
	Long: `Update OrbStack.

If an update is available, it will be downloaded and installed.
If there are no updates, this command will do nothing.

Updating is important to get the latest improvements, features, and security fixes.
This includes the Linux kernel, Docker, the CLI, GUI app, and other components.
`,
	Example: "  " + appid.ShortCtl + " update",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		bundlePath, err := updates.FindBundle()
		checkCLI(err)

		cmd := exec.Command("open", "-a", bundlePath, appid.UrlUpdate, "--args", "--check-updates")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Start()
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
