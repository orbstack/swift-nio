package cmd

import (
	"github.com/spf13/cobra"
)

var (
	flagZap bool
)

func init() {
	internalCmd.AddCommand(internalBrewUninstall)
	internalBrewUninstall.Flags().BoolVarP(&flagZap, "zap", "z", false, "")
}

var internalBrewUninstall = &cobra.Command{
	Use:    "brew-uninstall",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}
