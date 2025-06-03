package cmd

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(k8sCmd)
}

var k8sCmd = &cobra.Command{
	GroupID: groupContainers,
	Use:     "k8s",
	Short:   "Show commands for using Kubernetes",
	Long: `Show commands and instructions for using Kubernetes.
`,
	Example:           "  " + rootCmd.Use + " k8s",
	Args:              cobra.NoArgs,
	ValidArgsFunction: cobra.NoFileCompletions,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Printf(`To start a Kubernetes cluster:
    %[1]s start k8s
To stop the cluster:
    %[1]s stop k8s

kubectl is included:
    kubectl get pod -A

OrbStack's cluster is configured as the "orbstack" context for kubectl.
You can also find the kubeconfig at ~/.orbstack/k8s/config.yml.

To prevent OrbStack from changing the active kubectl context automatically:
    %[1]s config set docker.set_context false

For more info: https://orb.cx/k8s
`, rootCmd.Use)

		return nil
	},
}
