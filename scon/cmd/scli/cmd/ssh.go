package cmd

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/syssetup"
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
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Print(strings.Replace(`SSH command:
    ssh <HOST>
For more advanced usage:
    Connect to a specific machine: ssh MACHINE@<HOST>
    Log in as a specific user: ssh USER@MACHINE@<HOST>

Most apps (including Visual Studio Code and JetBrains Fleet) will work with the simple commands above. You can find "<HOST>" in the VS Code's Remote Explorer sidebar.

Applications that don't use OpenSSH (e.g. IntelliJ IDEA) will need the following settings:
    Host: localhost
    Port: 32222
    User: default
    Private key: ~/.orbstack/ssh/id_ed25519
For example:
    ssh -p 32222 -i ~/.orbstack/ssh/id_ed25519 default@localhost
`, "<HOST>", appid.ShortAppName, -1))

		if !syssetup.IsSshConfigWritable() {
			yellow := color.New(color.FgYellow)
			yellow.Println("\nWarning: SSH config is not writable. Add the following to your SSH config:")
			yellow.Println("    Include " + syssetup.MakeHomeRelative(conf.ExtraSshDir()+"/config"))
		}

		return nil
	},
}
