package cmd

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/orbstack/macvirt/scon/cmd/scli/dmigrate"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	vmgrutil "github.com/orbstack/macvirt/vmgr/util"
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
	migrateDockerCmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Disable filters and migrate everything")
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
		rawDockerSock := conf.DockerRemoteCtxSocket()
		if dmigrate.RemoteIsRunning(conf.DockerRemoteCtxSocketRaw()) {
			rawDockerSock = conf.DockerRemoteCtxSocketRaw()
		}

		destSocket := conf.DockerSocket()

		migrator, err := dmigrate.NewMigratorWithUnixSockets(srcSocket, destSocket)
		migrator.SetRawSrcSocket(rawDockerSock)
		checkCLI(err)

		err = migrator.MigrateAll(dmigrate.MigrateParams{
			All: flagAll,

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

		// open a conn to ddesktop socket, so we know when it's stopped
		desktopConn, err := net.Dial("unix", srcSocket)
		checkCLI(err)
		defer desktopConn.Close()

		// always quit remote so we can change context back after it stops
		logrus.Info("Stopping Docker Desktop")
		err = util.Run("osascript", "-e", `quit app "Docker Desktop"`)
		checkCLI(err)

		// wait for it to stop
		err = vmgrutil.WithTimeout1(func() error {
			var buf [1]byte
			_, err := desktopConn.Read(buf[:])
			return err
		}, dmigrate.RemoteStopTimeout)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// EOF = stopped

			// wait a bit for any processes to exit
			time.Sleep(1 * time.Second)

			// restore context for more seamless migration end
			err = util.Run(conf.FindXbin("docker"), "context", "use", appid.AppName)
			checkCLI(err)
		} else {
			logrus.Warnf("Docker Desktop did not stop in time: %v", err)
		}

		logrus.Info("Done")
		return nil
	},
}
