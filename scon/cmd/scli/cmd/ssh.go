package cmd

import (
	"fmt"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sshCmd)
}

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Show SSH details",
	Long: `Show commands and instructions for connecting to Linux via SSH.
Useful for remote editing (e.g. Visual Studio Code) or for connecting from another device.
`,
	Example: "  " + appid.ShortCtl + " ssh",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Printf(`SSH command:
    ssh macvirt
For more advanced usage:
	Connect to a specific machine: ssh MACHINE@macvirt
	Log in as a specific user: ssh USER@MACHINE@macvirt

Most apps (including Visual Studio Code and JetBrains Fleet) will work with the simple commands above. You can find "macvirt" in the VS Code's Remote Explorer sidebar.

Applications that don't use OpenSSH (e.g. IntelliJ IDEA) will need the following settings:
	Host: localhost
	Port: 62222
	User: default
	Private key: ~/.macvirt/ssh/id_ed25519
For example:
	ssh -p 62222 -i ~/.macvirt/ssh/id_ed25519 default@localhost
`)

		return nil
	},
}
