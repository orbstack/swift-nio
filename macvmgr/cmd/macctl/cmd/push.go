package cmd

import (
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
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
	if !strings.HasPrefix(p, mounts.VirtiofsMountpoint) {
		// assume home if not absolute
		if path.IsAbs(p) {
			// /home is likely a mistake
			if strings.HasPrefix(p, "/home/") {
				p = path.Join("/Users", p[6:])
			}
			p = mounts.VirtiofsMountpoint + p
		} else {
			// TODO work with other users
			p = mounts.VirtiofsMountpoint + "/Users/" + user + "/" + p
		}
	}

	return p
}

var pushCmd = &cobra.Command{
	Use:   "push Linux-source... macOS-dest",
	Short: "Copy files to macOS",
	Long: `Copy files from Linux to macOS.

Destination path is relative to the macOS user's home directory.`,
	Example: "  macctl push example.txt Desktop/",
	Args:    cobra.MatchAll(cobra.MinimumNArgs(2), cobra.OnlyValidArgs),
	RunE: func(_ *cobra.Command, args []string) error {
		// last = dest
		dest := args[len(args)-1]
		// rest = sources
		sources := args[:len(args)-1]

		dest = translateMacPath(dest)

		cmdArgs := []string{"-rf"}
		cmdArgs = append(cmdArgs, sources...)
		cmdArgs = append(cmdArgs, dest)

		// TODO: do this ourselves
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
