package cmd

import (
	"os"
	"strings"

	"github.com/orbstack/macvirt/macvmgr/cmd/macctl/hcli"
	"github.com/orbstack/macvirt/macvmgr/guihelper/guitypes"
	"github.com/spf13/cobra"
)

var (
	title    string
	subtitle string
	silent   bool
)

func init() {
	rootCmd.AddCommand(notifyCmd)
	notifyCmd.Flags().StringVarP(&title, "title", "t", "From Linux", "Notification title")
	notifyCmd.Flags().StringVarP(&subtitle, "subtitle", "s", "", "Notification subtitle")
	notifyCmd.Flags().BoolVarP(&silent, "silent", "S", false, "Don't play a sound")
}

var notifyCmd = &cobra.Command{
	Use:   "notify MESSAGE",
	Short: "Send a macOS notification",
	Long: `Send a desktop notification on macOS.

If multiple arguments are provided, they will be joined into a single message with spaces.`,
	Example: `  macctl notify "Command finished!"`,
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := hcli.Client().Notify(guitypes.Notification{
			Title:    title,
			Message:  strings.Join(args, " "),
			Subtitle: subtitle,
			Silent:   silent,
		})
		if err != nil {
			cmd.PrintErrln(err)
			os.Exit(1)
		}

		return nil
	},
}
