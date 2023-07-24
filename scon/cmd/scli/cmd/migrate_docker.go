package cmd

import (
	"errors"
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	//flagFromContext string
	flagImages     bool
	flagContainers bool
	flagVolumes    bool
)

func init() {
	migrateCmd.AddCommand(migrateDockerCmd)
	//migrateDockerCmd.Flags().StringVar(&flagFromContext, "from-context", "desktop-linux", "Context to migrate from")
	migrateDockerCmd.Flags().BoolVarP(&flagImages, "images", "i", true, "Migrate images")
	migrateDockerCmd.Flags().BoolVarP(&flagContainers, "containers", "c", true, "Migrate containers")
	migrateDockerCmd.Flags().BoolVarP(&flagVolumes, "volumes", "v", true, "Migrate volumes")
	migrateDockerCmd.Flags().BoolVarP(&flagForce, "force", "f", false, "Force migration even if OrbStack already has data")
	// no shorthand, only for gui use
	migrateDockerCmd.Flags().StringVar(&flagFormat, "format", "text", "Output format")
	migrateDockerCmd.Flags().MarkHidden("format")
}

var migrateDockerCmd = &cobra.Command{
	Use:   "docker",
	Short: "Migrate containers, volumes, and images from Docker Desktop",
	Long: `Migrate containers, volumes, images, and other data from Docker Desktop to OrbStack.
`,
	Example: "  " + appid.ShortCmd + " docker migrate",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if flagFormat == "json" {
			logrus.SetFormatter(&logrus.JSONFormatter{})
		}

		scli.EnsureSconVMWithSpinner()

		logrus.Info("Starting Docker Desktop")
		srcSocket := conf.DockerRemoteCtxSocket()
		remoteWasRunning := dmigrate.RemoteIsRunning(srcSocket)

		// start docker desktop if needed
		if !remoteWasRunning {
			err := util.Run("open", "-g" /*don't activate*/, "-j" /*hide*/, "-b", "com.docker.docker")
			checkCLI(err)

			// wait for start
			err = dmigrate.WaitForRemote(srcSocket)
			checkCLI(err)
		}

		// image slowpath: skip a proxy layer, for perf
		rawDockerSock := conf.DockerRemoteCtxSocketRaw()
		if _, err := os.Stat(rawDockerSock); err != nil {
			rawDockerSock = conf.DockerRemoteCtxSocket()
		}

		destSocket := conf.DockerSocket()

		migrator, err := dmigrate.NewMigratorWithUnixSockets(srcSocket, destSocket)
		migrator.SetRawSrcSocket(rawDockerSock)
		checkCLI(err)

		err = migrator.MigrateAll(dmigrate.MigrateParams{
			IncludeImages:     flagImages,
			IncludeContainers: flagContainers,
			IncludeVolumes:    flagVolumes,

			ForceIfExisting: flagForce,
		})
		if err != nil {
			if flagFormat == "json" && errors.Is(err, dmigrate.ErrEntitiesFailed) {
				// ignore - gui already got json log errors
			} else {
				checkCLI(err)
			}
		}

		// if we started remote, quit it
		if !remoteWasRunning {
			logrus.Info("Stopping Docker Desktop")
			err = util.Run("osascript", "-e", `quit app "Docker Desktop"`)
			checkCLI(err)
		}

		logrus.Info("Done")
		return nil
	},
}
