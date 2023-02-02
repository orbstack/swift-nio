package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(internalCmd)
}

var internalCmd = &cobra.Command{
	Use:    "internal",
	Short:  "Internal use only",
	Long:   `Commands for internal use only.`,
	Hidden: true,
}
