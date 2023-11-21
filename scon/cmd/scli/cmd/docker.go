package cmd

import (
	"fmt"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(dockerCmd)
}

var dockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Show commands for using Docker",
	Long: `Show commands and instructions for using Docker.
This includes building and running Docker containers, as well as using Docker Compose.
`,
	Example: "  " + appid.ShortCmd + " docker",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		fmt.Print(strings.Replace(`The "orb" and "orbctl" commands are for managing OrbStack and its machines.

To build and run containers and manage anything related to Docker, use the "docker" command directly from macOS:
    docker
There is no need to use machines if you only want to use Docker.

For example, to start an example container:
    docker run -p 80:80 docker/getting-started
Then visit http://localhost.

Compose and buildx are also included:
    docker compose
    docker buildx

To migrate containers, volumes, and images from Docker Desktop:
    orb migrate docker

OrbStack's cluster is configured as the "orbstack" context.
To prevent OrbStack from changing the active Docker context automatically:
	orb config set docker.set_context false

For more info: https://go.orbstack.dev/docker
`, "<HOST>", appid.AppName, -1))

		return nil
	},
}
