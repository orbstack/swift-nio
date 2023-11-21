package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(k8sCmd)
}

var k8sCmd = &cobra.Command{
	Use:   "k8s",
	Short: "Show commands for using Kubernetes",
	Long: `Show commands and instructions for using Kubernetes.
`,
	Example: "  " + appid.ShortCmd + " k8s",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Print(`To start a Kubernetes cluster:
    orb start k8s
To stop the cluster:
    orb stop k8s

kubectl is included:
    kubectl get pod -A

OrbStack's cluster is configured as the "orbstack" context for kubectl.
You can also find the kubeconfig at ~/.orbstack/k8s/config.yml.

To prevent OrbStack from changing the active kubectl context automatically:
    orb config set docker.set_context false

For more info: https://go.orbstack.dev/k8s
`)

		return nil
	},
}
