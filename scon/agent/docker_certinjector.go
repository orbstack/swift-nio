package agent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const rootCaCertName = "orbstack-root.crt"

var (
	caCertDirs = []string{
		"/etc/ssl/certs",
		"/usr/local/share/certs",
		"/etc/pki/tls/certs",
		"/etc/openssl/certs",
		"/var/ssl/certs",
	}
	// x: y means that directory y is created if path x exists, and the root cert is written to it
	conditionalCaCertDirs = map[string]string{
		"/usr/local":           "/usr/local/share/ca-certificates",
		"/etc/pki":             "/etc/pki/ca-trust/source/anchors",
		"/etc/ca-certificates": "/etc/ca-certificates/trust-source/anchors",
	}
	caCertFiles = []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/tls/cacert.pem",
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
		"/etc/ssl/cert.pem",
	}
)

type dockerCACertInjector struct {
	d *DockerAgent

	rootCertPem []byte

	// stores waitgroups for containers that are in progress of adding certs
	// uses full length container ID
	inProgressContainersMu *util.IDMutex[string]
}

func newDockerCACertInjector(d *DockerAgent) *dockerCACertInjector {
	return &dockerCACertInjector{
		d: d,

		inProgressContainersMu: &util.IDMutex[string]{},
	}
}

func (c *dockerCACertInjector) getRootCertPem() ([]byte, error) {
	if c.rootCertPem != nil {
		return c.rootCertPem, nil
	}

	certData, err := c.d.host.GetTLSRootData()
	if err != nil {
		return nil, fmt.Errorf("get root cert data: %w", err)
	}
	c.rootCertPem = []byte(certData.CertPEM)
	return c.rootCertPem, nil
}

func (c *dockerCACertInjector) addCertsToFs(fs *securefs.FS) error {
	rootCertPem, err := c.getRootCertPem()
	if err != nil {
		return err
	}

	for _, dirPath := range caCertDirs {
		if fi, err := fs.Stat(dirPath); err != nil || !fi.IsDir() {
			logrus.WithError(err).WithField("dirPath", dirPath).Debug("ca cert injector: dir does not exist or is not a directory, skipping")
			continue
		}

		err = fs.WriteFile(filepath.Join(dirPath, rootCaCertName), rootCertPem, 0o644)
		if err != nil {
			logrus.WithError(err).WithField("dirPath", dirPath).Error("ca cert injector: failed to write to dir, skipping")
			continue
		}
	}

	for _, filePath := range caCertFiles {
		file, err := fs.OpenFile(filePath, unix.O_RDWR|unix.O_APPEND, 0)
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Debug("ca cert injector: file does not exist, skipping")
			continue
		}
		defer file.Close()

		contents, err := io.ReadAll(file)
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Debug("ca cert injector: failed to read file, skipping")
			continue
		}

		if bytes.Contains(contents, rootCertPem) {
			logrus.WithError(err).WithField("filePath", filePath).Debug("ca cert injector: file already contains root cert, skipping")
			continue
		}

		_, err = file.Write(rootCertPem)
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Error("ca cert injector: failed to write to file, skipping")
			continue
		}
	}

	for conditionPath, targetDirPath := range conditionalCaCertDirs {
		if _, err := fs.Stat(conditionPath); err != nil {
			logrus.WithError(err).WithField("conditionPath", conditionPath).Debug("ca cert injector: condition path does not exist, skipping")
			continue
		}

		err = fs.MkdirAll(targetDirPath, 0o755)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("ca cert injector: failed to create target dir, skipping")
			continue
		}

		err = fs.WriteFile(filepath.Join(targetDirPath, rootCaCertName), rootCertPem, 0o644)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("ca cert injector: failed to write to target dir, skipping")
			continue
		}
	}

	return nil
}

func (c *dockerCACertInjector) addCertsToContainerImpl(ctr *dockertypes.ContainerJSON) error {
	if ctr.GraphDriver.Name != "overlay2" {
		logrus.WithField("container_id", ctr.ID).Warn("container is not using overlay2, skipping")
		return nil
	}

	logrus.WithField("ctr.ID", ctr.ID).Debug("ca cert injector: mounting overlay")

	// these have already been checked
	lowerDir := ctr.GraphDriver.Data["LowerDir"]
	upperDir := ctr.GraphDriver.Data["UpperDir"]
	workDir := ctr.GraphDriver.Data["WorkDir"]
	mergedDir := ctr.GraphDriver.Data["MergedDir"]
	orbMergedDir := filepath.Join(filepath.Dir(mergedDir), ".orbstack-merged")

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)

	didCreateDir := false
	didMount := false
	var fs *securefs.FS
	defer func() {
		if fs != nil {
			fs.Close()
		}
		if didMount {
			err := unix.Unmount(orbMergedDir, 0)
			if err != nil {
				logrus.WithError(err).WithField("mergedDir", orbMergedDir).Error("failed to unmount merged dir after adding certs")
			}
		}
		if didCreateDir {
			err := os.Remove(orbMergedDir)
			if err != nil {
				logrus.WithError(err).WithField("mergedDir", orbMergedDir).Error("failed to remove merged dir after adding certs")
			}
		}
	}()

	err := os.Mkdir(orbMergedDir, 0o755)
	if err != nil {
		logrus.WithError(err).WithField("mergedDir", orbMergedDir).Error("failed to create merged dir")
	}
	didCreateDir = true

	err = unix.Mount("orbstack", orbMergedDir, "overlay", unix.MS_NOATIME, opts)
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	didMount = true

	fs, err = securefs.NewFromPath(orbMergedDir)
	if err != nil {
		return fmt.Errorf("open securefs: %w", err)
	}

	logrus.WithField("container_id", ctr.ID).Debug("ca cert injector: mounted overlay")

	return c.addCertsToFs(fs)
}

func (c *dockerCACertInjector) addCertsToContainer(containerID string) (_ string, retErr error) {
	// check if container is stopped before we try to acquire the in progress status
	ctr, err := c.d.client.InspectContainer(containerID)
	if err != nil {
		return "", err
	}

	if ctr.State.Status == "running" || ctr.State.Status == "paused" || ctr.State.Status == "restarting" || ctr.State.Status == "removing" {
		logrus.WithFields(logrus.Fields{
			"container_id": ctr.ID,
			"status":       ctr.State.Status,
		}).Debug("container is not in a state to add certs, skipping")
		return "", nil
	}

	c.inProgressContainersMu.Lock(ctr.ID)
	defer func() {
		if retErr != nil {
			c.containerNotInProgress(ctr.ID)
		}
	}()

	// check for all the paths early since we need some of them and then we don't need to check later
	_, ok := ctr.GraphDriver.Data["LowerDir"]
	if !ok {
		return "", fmt.Errorf("container %s is using overlay2 but has no lowerdir", ctr.ID)
	}
	_, ok = ctr.GraphDriver.Data["WorkDir"]
	if !ok {
		return "", fmt.Errorf("container %s is using overlay2 but has no workdir", ctr.ID)
	}
	_, ok = ctr.GraphDriver.Data["UpperDir"]
	if !ok {
		return "", fmt.Errorf("container %s is using overlay2 but has no upperdir", ctr.ID)
	}
	mergedDir, ok := ctr.GraphDriver.Data["MergedDir"]
	if !ok {
		return "", fmt.Errorf("container %s is using overlay2 but has no merged dir", ctr.ID)
	}

	// check if container is still stopped before we proceed
	// otherwise, we have a race:
	// - [1] container start request
	// - [1] certs are added to container
	// - [2] container start request
	// - [2] container running status is checked and passes
	// - [1] container starts
	// - [1] container is marked as no longer in progress
	// - [2] not in progress check passes, cert add routine begins
	// - [2] overlayfs is mounted again even though container is already running
	// - ub! :D

	var parentStat unix.Stat_t
	err = unix.Stat(filepath.Dir(mergedDir), &parentStat)
	if err != nil {
		return "", fmt.Errorf("failed to stat merged dir parent: %w", err)
	}
	var dirStat unix.Stat_t
	err = unix.Stat(mergedDir, &dirStat)
	if err == nil && parentStat.Dev != dirStat.Dev {
		// dir is already mounted, abort
		c.containerNotInProgress(ctr.ID)
		return "", nil
	}

	err = c.addCertsToContainerImpl(ctr)
	if err != nil {
		return "", err
	}

	// note, container is marked as not in progress after start request finishes
	return ctr.ID, nil
}

// we can only mark a container as no longer in progress after it has finished starting
// otherwise we have a race:
// - [1] container start request
// - [1] certs are added to container
// - [1] container is marked as no longer in progress
// - [1] start request is allowed to proceed
// - [2] container start request
// - [2] not running check passes, cert add routine begins
// - [1] container start request is processed by docker, docker mounts overlay
// - [2] cert add routine mounts overlay
// - ub! :D
func (c *dockerCACertInjector) containerNotInProgress(containerID string) {
	c.inProgressContainersMu.Unlock(containerID)
}

func (a *AgentServer) DockerAddCertsToContainer(containerID string, reply *string) error {
	notifyCtrId, err := a.docker.caCertInjector.addCertsToContainer(containerID)
	if err != nil {
		return err
	}
	*reply = notifyCtrId
	return nil
}

func (a *AgentServer) DockerNotifyCACertInjectorStartFinished(containerID string, reply *None) error {
	a.docker.caCertInjector.containerNotInProgress(containerID)
	return nil
}
