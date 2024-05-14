package cmd

import (
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/orbstack/macvirt/vmgr/cmd/macctl/shell"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pushCmd)
}

func translateMacPath(p string) string {
	// clean path
	p = path.Clean(p)
	user := shell.HostUser()
	if user == "" {
		user = os.Getenv("USER")
	}

	// translate p if user didn't already prefix it
	if !strings.HasPrefix(p, mounts.Virtiofs) {
		// assume home if not absolute
		if path.IsAbs(p) {
			// /home is likely a mistake
			if strings.HasPrefix(p, "/home/") {
				p = path.Join("/Users", strings.TrimPrefix(p, "/home/"))
			}
			p = mounts.Virtiofs + p
		} else {
			p = mounts.Virtiofs + "/Users/" + user + "/" + p
		}
	}

	return p
}

var pushCmd = &cobra.Command{
	Use:   "push Linux-source... macOS-dest",
	Short: "Copy files to macOS",
	Long: `Copy files from Linux to macOS.

Destination path is relative to the macOS user's home directory.

This is provided for convenience, but we recommend using shared folders for simplicity. For example:
    macctl push example.txt Downloads/
is equivalent to:
    cp example.txt /Users/$USER/Downloads/
`,
	Example: "  macctl push example.txt Desktop/",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		var dest string
		var sources []string
		if len(args) == 1 {
			// assume dest is home
			dest = "."
			sources = args
		} else {
			// last = dest
			dest = args[len(args)-1]
			// rest = sources
			sources = args[:len(args)-1]
		}

		dest = translateMacPath(dest)

		cmdArgs := []string{"-rf"}
		cmdArgs = append(cmdArgs, sources...)
		cmdArgs = append(cmdArgs, dest)

		// TODO: do this ourselves
		// exec OK: this runs on Linux, in a separate process
		cmd := exec.Command("cp", cmdArgs...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			} else {
				return err
			}
		}

		return nil
	},
}
