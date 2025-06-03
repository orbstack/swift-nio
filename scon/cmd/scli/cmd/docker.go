package cmd

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(dockerCmd)
}

var dockerCmd = &cobra.Command{
	GroupID: groupContainers,
	Use:     "docker",
	Short:   "Extension commands for Docker",
	Long: `The "orb" and "orbctl" commands are primarily for managing OrbStack and its Linux machines.

To build and run containers and manage anything related to Docker, use the "docker" command directly from macOS.
You don't need a Linux machine to use Docker.

For example, to start an example container:
    docker run -p 80:80 docker/getting-started
Then visit http://localhost.

Compose and buildx are also included:
    docker compose
    docker buildx

OrbStack's Docker engine is configured as the "orbstack" context.
To prevent OrbStack from changing the active Docker context automatically:
	orb config set docker.set_context false

For more info: https://orb.cx/docker

Below are OrbStack commands that *extend* the core Docker CLI functionality.
`,
}
