package cmd

import (
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
	"github.com/spf13/cobra"
)

const script = `display notification (system attribute "__MV_NOTIFY_DESC") with title (system attribute "__MV_NOTIFY_TITLE")`

var (
	title string
)

func init() {
	rootCmd.AddCommand(notifyCmd)
	notifyCmd.Flags().StringVarP(&title, "title", "t", "", "Notification title")
}

var notifyCmd = &cobra.Command{
	Use:   "notify MESSAGE",
	Short: "Send a macOS notification",
	Long: `Send a desktop notification on macOS.

If multiple arguments are provided, they will be joined into a single message with spaces.
Use --title to set a title.`,
	Example: `  macctl notify "Command finished!"`,
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs: []string{"osascript", "-e", script},
			ExtraEnv: map[string]string{
				"__MV_NOTIFY_DESC":  strings.Join(args, " "),
				"__MV_NOTIFY_TITLE": title,
			},
		})
		if err != nil {
			panic(err)
		}

		os.Exit(exitCode)
		return nil
	},
}
