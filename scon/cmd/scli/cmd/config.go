package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configure the Linux virtual machine",
	Long:  `Get or set configuration options for the Linux virtual machine.`,
}
