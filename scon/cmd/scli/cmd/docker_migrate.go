package cmd

import (
	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/spf13/cobra"
)

var (
	//flagFromContext string
	flagImages     bool
	flagContainers bool
	flagVolumes    bool
)

func init() {
	dockerCmd.AddCommand(dockerMigrateCmd)
	//dockerMigrateCmd.Flags().StringVar(&flagFromContext, "from-context", "desktop-linux", "Context to migrate from")
	dockerMigrateCmd.Flags().BoolVar(&flagImages, "images", true, "Migrate images")
	dockerMigrateCmd.Flags().BoolVar(&flagContainers, "containers", true, "Migrate containers")
	dockerMigrateCmd.Flags().BoolVar(&flagVolumes, "volumes", true, "Migrate volumes")
}

var dockerMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate containers, images, and volumes from Docker Desktop",
	Long: `Migrate containers, images, volumes, and other data from Docker Desktop to OrbStack.
`,
	Example: "  " + appid.ShortCtl + " docker migrate",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		scli.EnsureSconVMWithSpinner()

		srcSocket := coredir.HomeDir() + "/.docker/run/docker.sock"
		destSocket := conf.DockerSocket()

		migrator, err := dmigrate.NewMigratorWithUnixSockets(srcSocket, destSocket)
		checkCLI(err)

		err = migrator.MigrateAll(dmigrate.MigrateParams{
			IncludeImages:     flagImages,
			IncludeContainers: flagContainers,
			IncludeVolumes:    flagVolumes,
		})
		checkCLI(err)

		return nil
	},
}
