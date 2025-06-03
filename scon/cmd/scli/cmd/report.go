package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/bugreport"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(reportCmd)
}

var reportCmd = &cobra.Command{
	GroupID: groupGeneral,
	Use:     "report",
	Short:   "Gather info for a bug report",
	Long: `Gather OrbStack and system information for reporting bugs.

Issue tracker: https://github.com/orbstack/orbstack/issues
Privacy policy (including what info is collected): https://orbstack.dev/privacy#diagnostic-reports

You can review the generated report at ~/.orbstack/diag.
`,
	Example: "  " + rootCmd.Use + " report",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var buffer bytes.Buffer

		err := bugreport.GenerateInfo(&buffer)
		checkCLI(err)

		// generate zip w/ spinner
		spinner := spinutil.Start("green", "Generating diagnostic report...")
		zipPath, pkg, err := bugreport.Build(buffer.Bytes())
		spinner.Stop()
		checkCLI(err)

		cmd.PrintErrln("Diagnostic report saved to: " + zipPath)
		color.New(color.Bold).Print("Review report contents before uploading [y/N]? ")
		var resp string
		_, err = fmt.Scanln(&resp)
		if err != nil && !errors.Is(err, io.EOF) && err.Error() != "unexpected newline" {
			checkCLI(err)
		}

		lower := strings.ToLower(resp)
		if lower != "" && lower != "n" && lower != "no" {
			err = bugreport.Extract(zipPath)
			checkCLI(err)

			err = exec.Command("open", strings.TrimSuffix(zipPath, ".zip")).Run()
			checkCLI(err)

			cmd.PrintErrln("\nReport opened in a new Finder window.")
			color.New(color.Bold).Print("Upload [Y/n]? ")

			// ask for upload confirmation
			var resp string
			_, err = fmt.Scanln(&resp)
			if err != nil && !errors.Is(err, io.EOF) && err.Error() != "unexpected newline" {
				checkCLI(err)
			}

			cmd.PrintErrln()

			lower := strings.ToLower(resp)
			if lower != "" && lower != "y" && lower != "yes" {
				checkCLI(errors.New("Aborted."))
			}
		}

		spinner = spinutil.Start("blue", "Uploading report...")
		downloadURL, err := pkg.Upload()
		spinner.Stop()
		checkCLI(err)

		fmt.Fprintf(&buffer, "Full report: %s", downloadURL)
		fmt.Println(string(buffer.Bytes()))
		fmt.Println("")
		fmt.Println("---------------- [ cut here ] ----------------")
		fmt.Println()
		fmt.Println("Please copy and paste this into your bug report.")
		fmt.Println("Open an issue here: https://github.com/orbstack/orbstack/issues/new/choose")
		fmt.Println()

		// copy to clipboard
		copyCmd := exec.Command("pbcopy")
		copyCmd.Stdin = &buffer
		err = copyCmd.Run()
		if err != nil {
			fmt.Printf("Failed to copy to clipboard: %v\n", err)
			return nil
		}

		greenBold := color.New(color.FgGreen, color.Bold).SprintFunc()
		fmt.Println(greenBold("✅ Copied to clipboard!"))

		return nil
	},
}
