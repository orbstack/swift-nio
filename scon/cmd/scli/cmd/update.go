package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/drm/updates"
	"github.com/spf13/cobra"
)

var (
	flagCheck bool
)

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolVarP(&flagCheck, "check", "c", false, "Check for updates, but don't install. Returns exit status 3 if outdated and 0 if not.")
}

var updateCmd = &cobra.Command{
	GroupID: groupGeneral,
	Use:     "update",
	Short:   "Update OrbStack",
	Long: `Update OrbStack.

If an update is available, it will be downloaded and installed.
If there are no updates, this command will do nothing.

Updating is important to get the latest improvements, features, and security fixes.
This includes the Linux kernel, Docker, the CLI, GUI app, and other components.
`,
	Example: "  " + rootCmd.Use + " update",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, args []string) error {
		bundlePath, err := conf.FindAppBundle()
		checkCLI(err)

		if flagCheck {
			updateInfo, err := updates.CheckSparkleCLI()
			checkCLI(err)

			if updateInfo.Available {
				fmt.Println("Update available!")
				os.Exit(3)
			} else {
				fmt.Println("OrbStack is up to date.")
				os.Exit(0)
			}
		}

		// TODO: fix sparkle-cli update
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

		fmt.Println("OrbStack updater will open in a new window.")
		return nil
	},
}
