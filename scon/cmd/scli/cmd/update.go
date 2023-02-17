package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(updateCmd)
}

func findSparkleExe() (string, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", err
	}

	// resolve symlinks
	selfExe, err = filepath.EvalSymlinks(selfExe)
	if err != nil {
		return "", err
	}

	if conf.Debug() {
		return "/Users/dragon/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/SourcePackages/artifacts/sparkle/sparkle.app/Contents/MacOS/sparkle", nil
	}

	return path.Join(path.Dir(selfExe), "sparkle-cli"), nil
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
	RunE: func(cmd *cobra.Command, args []string) error {
		sparkleExe, err := findSparkleExe()
		checkCLI(err)

		feedURL := fmt.Sprintf("https://api-updates.orbstack.dev/%s/appcast.xml", runtime.GOARCH)
		ver := appver.Get()
		userAgent := fmt.Sprintf("sparkle-cli vmgr/%s/%d/%s/%s", ver.Short, ver.Code, ver.GitDescribe, ver.GitCommit)
		bundlePath := strings.TrimSuffix(path.Dir(sparkleExe), "/Contents/MacOS")

		sparkleCmd := exec.Command(sparkleExe, "--check-immediately", "--user-agent-name", userAgent, "--feed-url", feedURL, "--send-profile", "--grant-automatic-checks", "--interactive", "--channels", "beta", "--allow-major-upgrades", bundlePath)
		sparkleCmd.Stdout = os.Stdout
		sparkleCmd.Stderr = os.Stderr
		err = sparkleCmd.Run()
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
