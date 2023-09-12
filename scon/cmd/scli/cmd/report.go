package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/bugreport"
	"github.com/orbstack/macvirt/scon/cmd/scli/osutil"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/conf/mem"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(reportCmd)
	// quiet is for GUI bug report flow
	reportCmd.Flags().BoolVarP(&flagQuiet, "quiet", "q", false, "Quiet mode")
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Gather info for a bug report",
	Long: `Gather OrbStack and system information for reporting bugs.

Issue tracker: https://github.com/orbstack/orbstack/issues
Privacy policy (including what info is collected): https://orbstack.dev/privacy#diagnostic-reports

You can review the generated report at ~/.orbstack/diag.
`,
	Example: "  " + appid.ShortCmd + " report",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var buffer bytes.Buffer
		writer := io.MultiWriter(os.Stdout, &buffer)

		fmt.Fprintln(writer, "OrbStack info:")
		ver := appver.Get()
		fmt.Fprintf(writer, "  Version: %s\n", ver.Short)
		fmt.Fprintf(writer, "  Commit: %s (%s)\n", ver.GitCommit, ver.GitDescribe)
		fmt.Fprintln(writer, "")

		fmt.Fprintln(writer, "System info:")
		osVerCode, err := osutil.OsVersionCode()
		checkCLI(err)
		osProductVer, err := osutil.OsProductVersion()
		checkCLI(err)
		fmt.Fprintf(writer, "  macOS: %s (%s)\n", osProductVer, osVerCode)
		fmt.Fprintf(writer, "  CPU: %s, %d cores\n", runtime.GOARCH, runtime.NumCPU())
		cpuModel, err := osutil.CpuModel()
		checkCLI(err)
		fmt.Fprintf(writer, "  CPU model: %s\n", cpuModel)
		machineModel, err := osutil.MachineModel()
		checkCLI(err)
		fmt.Fprintf(writer, "  Model: %s\n", machineModel)
		fmt.Fprintf(writer, "  Memory: %d GiB\n", mem.PhysicalMemory()/1024/1024/1024)
		fmt.Fprintln(writer, "")

		// generate zip w/ spinner
		spinner := spinutil.Start("green", "Generating diagnostic report...")
		downloadURL, err := bugreport.BuildAndUpload(buffer.Bytes())
		spinner.Stop()
		if err != nil {
			fmt.Fprintln(writer, "Diagnostic report failed:", err)
		} else {
			fmt.Fprintf(writer, "Full report: %s\n", downloadURL)
		}
		fmt.Fprintln(writer, "<!-- (To review the report, check ~/.orbstack/diag) -->")

		// stop writing to buffer after this point
		writer = os.Stdout

		if !flagQuiet {
			fmt.Fprintln(writer, "")
			fmt.Fprintln(writer, "---------------- [ cut here ] ----------------")
			fmt.Fprintln(writer, "")
			fmt.Fprintln(writer, "Please copy and paste this into your bug report.")
			fmt.Fprintln(writer, "Open an issue here: https://github.com/orbstack/orbstack/issues/new/choose")
			fmt.Fprintln(writer, "")

			// copy to clipboard
			copyCmd := exec.Command("pbcopy")
			copyCmd.Stdin = &buffer
			err = copyCmd.Run()
			if err != nil {
				fmt.Fprintln(writer, "Failed to copy to clipboard:", err)
			}

			// print copied
			greenBold := color.New(color.FgGreen, color.Bold).SprintFunc()
			fmt.Fprintln(writer, greenBold("✅ Copied to clipboard!"))
		}

		return nil
	},
}
