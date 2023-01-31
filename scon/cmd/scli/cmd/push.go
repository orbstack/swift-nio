package cmd

import (
	"os"
	"os/exec"
	"os/user"
	"path"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pushCmd)
	pushCmd.Flags().StringVarP(&flagContainer, "container", "c", "", "Copy to a specific container")
}

func translateLinuxPath(container, p string) string {
	// clean path
	p = path.Clean(p)
	nfsDir := conf.NfsMountpoint() + "/" + container

	// assume user on linux side. for other users, we expect the user to use absolute path
	u, err := user.Current()
	if err != nil {
		panic(err)
	}
	user := u.Username
	userHome := "/home/" + user // TODO ask scon agent

	// translate p if user didn't already prefix it
	if !strings.HasPrefix(p, nfsDir) {
		// assume home if not absolute
		if path.IsAbs(p) {
			// /Users is likely a mistake
			if strings.HasPrefix(p, "/Users/") {
				p = path.Join("/home", p[6:])
			}
			p = nfsDir + p
		} else {
			p = nfsDir + userHome + "/" + p
		}
	}

	return p
}

var pushCmd = &cobra.Command{
	Use:   "push [OPTIONS] macOS-source... Linux-dest",
	Short: "Copy files to Linux",
	Long: `Copy files from macOS to Linux.

Destination path is relative to the Linux user's home directory.`,
	Example: "  " + appid.ShortCtl + " push example.txt Desktop/",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		// last = dest
		dest := args[len(args)-1]
		// rest = sources
		sources := args[:len(args)-1]

		containerName := flagContainer
		if containerName == "" {
			c, err := scli.Client().GetDefaultContainer()
			check(err)
			containerName = c.Name
		}

		dest = translateLinuxPath(containerName, dest)

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
