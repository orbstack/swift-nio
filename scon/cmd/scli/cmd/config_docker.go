package cmd

import (
	"os"
	"os/exec"
	"strings"

	"github.com/orbstack/macvirt/macvmgr/conf"
	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/vmclient"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/spf13/cobra"
)

func init() {
	configCmd.AddCommand(configDockerCmd)
}

var configDockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Edit Docker engine configuration",
	Long: `Open the Docker engine configuration file for editing.

This will open ~/.orbstack/config/docker.json in your default command line text editor ($EDITOR).
If changes are made, the Docker engine will be restarted.
`,
	Example: "  " + appid.ShortCtl + " docker",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := conf.DockerDaemonConfig()
		preferredEditor := os.Getenv("EDITOR")
		if preferredEditor == "" {
			preferredEditor = "nano"
		}

		// read preimage
		preConfig, err := os.ReadFile(configPath)
		checkCLI(err)

		editorCombinedArgs := strings.Fields(preferredEditor)
		editorCombinedArgs = append(editorCombinedArgs, configPath)
		eCmd := exec.Command(editorCombinedArgs[0], editorCombinedArgs[1:]...)
		eCmd.Stdin = os.Stdin
		eCmd.Stdout = os.Stdout
		eCmd.Stderr = os.Stderr
		err = eCmd.Run()
		checkCLI(err)

		// read postimage
		postConfig, err := os.ReadFile(configPath)
		checkCLI(err)

		// restart if changed and vm is running
		if string(preConfig) != string(postConfig) && vmclient.IsRunning() {
			// ensure scon
			scli.EnsureSconVMWithSpinner()

			// restart docker
			record, err := scli.Client().GetByName("docker")
			checkCLI(err)
			spinner := spinutil.Start("green", "Restarting Docker")
			err = scli.Client().ContainerRestart(record)
			spinner.Stop()
			checkCLI(err)
		}

		return nil
	},
}
