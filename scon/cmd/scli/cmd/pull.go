package cmd

import (
	"os"
	"os/exec"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
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

This is provided for convenience, but we recommend using shared folders for simplicity. For example:
    ` + appid.ShortCtl + ` pull code/example.txt .
is equivalent to:
	cp ~/Linux/ubuntu/home/$USER/code/example.txt .`,
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

		for i, source := range sources {
			sources[i] = translateLinuxPath(containerName, source)
		}

		// ignore xattr - nfs can't handle it
		cmdArgs := []string{"-rfX"}
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
