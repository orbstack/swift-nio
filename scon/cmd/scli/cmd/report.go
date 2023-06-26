package cmd

import (
	"fmt"
	"runtime"

	"github.com/orbstack/macvirt/scon/cmd/scli/osutil"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(reportCmd)
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Gather info for a bug report",
	Long: `Gather OrbStack and system information for reporting bugs.

Issue tracker: https://github.com/orbstack/orbstack/issues
`,
	Example: "  " + appid.ShortCtl + " report",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("OrbStack info:")
		ver := appver.Get()
		fmt.Printf("  Version: %s (%d)\n", ver.Short, ver.Code)
		fmt.Printf("  Commit: %s (%s)\n", ver.GitCommit, ver.GitDescribe)
		fmt.Println("")

		fmt.Println("System info:")
		osVerCode, err := osutil.OsVersionCode()
		checkCLI(err)
		osProductVer, err := osutil.OsProductVersion()
		checkCLI(err)
		fmt.Printf("  macOS: %s (%s)\n", osProductVer, osVerCode)
		fmt.Printf("  CPU: %s, %d cores\n", runtime.GOARCH, runtime.NumCPU())
		cpuModel, err := osutil.CpuModel()
		checkCLI(err)
		fmt.Printf("  CPU model: %s\n", cpuModel)
		fmt.Println("")

		// TODO: settings

		fmt.Println("---------------- [ cut here ] ----------------")
		fmt.Println("")
		fmt.Println("Please copy and paste the above information into your bug report.")
		fmt.Println("Open an issue here: https://github.com/orbstack/orbstack/issues/new/choose")
		fmt.Println("")

		return nil
	},
}
