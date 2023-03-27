package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(internalCmd)
}

var internalCmd = &cobra.Command{
	Use:    "_internal",
	Hidden: true,
}
