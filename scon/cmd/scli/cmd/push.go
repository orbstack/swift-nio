package cmd

import (
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/shell"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/sshpath"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pushCmd)
	pushCmd.Flags().StringVarP(&flagMachine, "machine", "m", "", "Copy to a specific machine")
}

var pushCmd = &cobra.Command{
	Use:   "push [flags] macOS-source... [Linux-dest]",
	Short: "Copy files to Linux",
	Long: `Copy files from macOS to Linux.

Destination path is relative to the Linux user's home directory.
If destination is not specified, the home directory is used.

This is provided for convenience, but you can also use shared folders. For example:
    ` + appid.ShortCtl + ` push example.txt code/
is equivalent to:
	cp example.txt ~/OrbStack/ubuntu/home/$USER/code/`,
	Example: "  " + appid.ShortCtl + " push example.txt Desktop/",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

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

		containerName := flagMachine
		if containerName == "" {
			c, err := scli.Client().GetDefaultContainer()
			checkCLI(err)
			containerName = c.Name
		}

		// /mnt/mac
		for i, src := range sources {
			sources[i] = sshpath.ToLinux(src, sshpath.ToLinuxOptions{
				TargetContainer: containerName,
			})
		}

		// special case of translation: ~/ in dest -> relative to Linux home
		macHome, err := os.UserHomeDir()
		checkCLI(err)
		if dest == macHome {
			dest = "."
		} else if strings.HasPrefix(dest, macHome+"/") {
			dest = "." + strings.TrimPrefix(dest, macHome)
		}

		ret, err := shell.CopyFiles(containerName, sources, dest)
		checkCLI(err)
		os.Exit(ret)

		return nil
	},
}
