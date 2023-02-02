package cmd

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(pullCmd)
}

var pullCmd = &cobra.Command{
	Use:   "pull macOS-source... Linux-dest",
	Short: "Copy files from macOS",
	Long: `Copy files from macOS to Linux.

Source paths are relative to the macOS user's home directory.

This is provided for convenience, but we recommend using shared folders for simplicity. For example:
    macctl pull Downloads/example.txt .
is equivalent to:
    cp /Users/$USER/Downloads/example.txt .
`,
	Example: "  macctl pull Desktop/example.txt .",
	Args:    cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		// last = dest
		dest := args[len(args)-1]
		// rest = sources
		sources := args[:len(args)-1]

		for i, source := range sources {
			sources[i] = translateMacPath(source)
		}

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
