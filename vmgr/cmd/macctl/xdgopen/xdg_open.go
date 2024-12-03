package xdgopen

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/orbstack/macvirt/vmgr/cmd/macctl/shell"
	"github.com/orbstack/macvirt/vmgr/conf/sshpath"
	"github.com/spf13/cobra"
)

var (
	flagManual  bool
	flagVersion bool
)

func init() {
	xdgOpenCmd.Flags().BoolVarP(&flagManual, "manual", "", false, "Open the manual page")
	xdgOpenCmd.Flags().BoolVarP(&flagVersion, "version", "", false, "Print the version number")
}

var xdgOpenCmd = &cobra.Command{
	Use:   "xdg-open",
	Short: "Open files and URLs on macOS (OrbStack implementation)",
	Long: `Open files and URLs on macOS.

This is the OrbStack implementation of xdg-open.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagVersion {
			fmt.Println("xdg-open 0.1.0-orbstack")
			return nil
		}

		if flagManual {
			cmd.Help()
			return nil
		}

		if len(args) != 1 {
			return fmt.Errorf("accepts 1 arg, received %d", len(args))
		}

		target := args[0]

		isURL := false
		u, err := url.Parse(target)
		if err == nil {
			if u.Scheme != "" && u.Scheme != "file" {
				isURL = true
			} else if u.Scheme == "file" {
				target = strings.TrimPrefix(target, "file://")
			}
		}

		// resolve and translate file paths
		if !isURL {
			_, err = os.Stat(target)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(os.Stderr, "xdg-open: file '%s' does not exist\n", target)
					// exit codes from `man xdg-open`
					os.Exit(2)
				} else {
					fmt.Fprintf(os.Stderr, "xdg-open: failed to open '%s': %v\n", target, err)
					os.Exit(4)
				}
			}

			target, err = filepath.EvalSymlinks(target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xdg-open: failed to resolve path '%s': %v\n", target, err)
				os.Exit(4)
			}

			target, err = filepath.Abs(target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "xdg-open: failed to resolve path '%s': %v\n", target, err)
				os.Exit(4)
			}

			target = sshpath.ToMac(target, shell.MakePathTransOptions())
		}

		// skip command stub: we've already translated paths
		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs: []string{"open", target},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "xdg-open: %v\n", err)
			// fallthrough to exit code
		}

		os.Exit(exitCode)
		return nil
	},
}

func RunXdgOpenStub() (int, error) {
	err := xdgOpenCmd.Execute()
	if err != nil {
		// cobra already prints error
		return 1, nil
	}
	return 0, nil
}
