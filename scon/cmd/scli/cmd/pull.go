package cmd

import (
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pullCmd)
	pullCmd.Flags().StringVarP(&flagMachine, "machine", "m", "", "Copy to a specific machine")
}

var pullCmd = &cobra.Command{
	Use:   "pull [flags] macOS-source... [Linux-dest]",
	Short: "Copy files from Linux",
	Long: `Copy files from Linux to macOS.

Source paths are relative to the Linux user's home directory.
If destination is not specified, the current directory is used.

This is provided for convenience, but you can also use shared folders. For example:
    ` + appid.ShortCtl + ` pull code/example.txt .
is equivalent to:
	cp ~/OrbStack/ubuntu/home/$USER/code/example.txt .`,
	Example: "  " + appid.ShortCtl + " pull code/example.txt .",
	Args:    cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		var dest string
		var sources []string
		if len(args) == 1 {
			// assume dest is cwd
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

		// special case of translation: ~/ in sources -> relative to Linux home
		macHome, err := os.UserHomeDir()
		checkCLI(err)
		for i, src := range sources {
			if src == macHome {
				sources[i] = "."
			} else if strings.HasPrefix(src, macHome+"/") {
				sources[i] = "." + strings.TrimPrefix(src, macHome)
			}
		}

		// to /mnt/mac
		dest = shell.TranslatePath(dest, containerName)

		ret, err := shell.CopyFiles(containerName, sources, dest)
		checkCLI(err)
		os.Exit(ret)

		return nil
	},
}
