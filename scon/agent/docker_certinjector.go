package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/syncx"
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
	// x: y means that y is created if x exists
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
	inProgressContainersMu syncx.Mutex
	inProgressContainers   map[string]*sync.WaitGroup
}

func newDockerCACertInjector(d *DockerAgent) *dockerCACertInjector {
	return &dockerCACertInjector{
		d: d,

		inProgressContainers: make(map[string]*sync.WaitGroup),
	}
}

func (c *dockerCACertInjector) addCertsToFs(fs *securefs.FS) error {
	if c.rootCertPem == nil {
		certData, err := c.d.host.GetTLSRootData()
		if err != nil {
			return fmt.Errorf("get root cert data: %w", err)
		}
		c.rootCertPem = []byte(certData.CertPEM)
	}

	for _, dir := range caCertDirs {
		if fi, err := fs.Stat(dir); err == nil && fi.IsDir() {
			err := fs.WriteFile(filepath.Join(dir, rootCaCertName), c.rootCertPem, 0o644)
			if err != nil {
				logrus.WithError(err).WithField("dir", dir).Error("failed to write root cert to ca cert dir")
			}
		}
	}

	for _, file := range caCertFiles {
		if file, err := fs.OpenFile(file, unix.O_RDWR, 0o644); err == nil {
			var contents []byte
			_, err := file.Read(contents)
			if err != nil {
				logrus.WithError(err).WithField("file", file).Error("failed to read ca cert file")
				continue
			}

			if bytes.Contains(contents, c.rootCertPem) {
				continue
			}

			contents = append(contents, c.rootCertPem...)

			_, err = file.WriteAt(contents, 0)
			if err != nil {
				logrus.WithError(err).WithField("file", file).Error("failed to append to ca cert file")
			}
			file.Close()
		}
	}

	for dir, targetDir := range conditionalCaCertDirs {
		if _, err := fs.Stat(dir); err == nil {
			err := fs.MkdirAll(targetDir, 0o755)
			if err != nil {
				logrus.WithError(err).WithField("dir", targetDir).Error("failed to create conditional ca cert dir")
			} else {
				err := fs.WriteFile(filepath.Join(targetDir, rootCaCertName), c.rootCertPem, 0o644)
				if err != nil {
					logrus.WithError(err).WithField("dir", targetDir).Error("failed to write root cert to conditional ca cert dir")
				}
			}
		}
	}

	return nil
}

func (c *dockerCACertInjector) addCertsToContainerImpl(ctr *dockertypes.ContainerJSON) error {
	if ctr.GraphDriver.Name != "overlay2" {
		logrus.WithField("container_id", ctr.ID).Warn("container is not using overlay2, skipping")
		return nil
	}

	logrus.WithField("container_id", ctr.ID).Info("soweli | ================ mounting ================")

	lowerDir, ok := ctr.GraphDriver.Data["LowerDir"]
	if !ok {
		return fmt.Errorf("container %s is using overlay2 but has no lowerdir", ctr.ID)
	}
	upperDir, ok := ctr.GraphDriver.Data["UpperDir"]
	if !ok {
		return fmt.Errorf("container %s is using overlay2 but has no upperdir", ctr.ID)
	}
	workDir, ok := ctr.GraphDriver.Data["WorkDir"]
	if !ok {
		return fmt.Errorf("container %s is using overlay2 but has no workdir", ctr.ID)
	}
	mergedDir, ok := ctr.GraphDriver.Data["MergedDir"]
	if !ok {
		return fmt.Errorf("container %s is using overlay2 but has no merged-ca-certs", ctr.ID)
	}
	mergedDir = filepath.Join(filepath.Dir(mergedDir), ".orbstack-merged")

	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)

	didCreateDir := false
	didMount := false
	var fs *securefs.FS
	defer func() {
		if fs != nil {
			fs.Close()
		}
		if didMount {
			err := unix.Unmount(mergedDir, 0)
			if err != nil {
				logrus.WithError(err).WithField("mergedDir", mergedDir).Error("failed to unmount merged dir after adding certs")
			}
		}
		if didCreateDir {
			err := os.Remove(mergedDir)
			if err != nil {
				logrus.WithError(err).WithField("mergedDir", mergedDir).Error("failed to remove merged dir after adding certs")
			}
		}
	}()

	err := os.Mkdir(mergedDir, 0o755)
	if err != nil {
		logrus.WithError(err).WithField("mergedDir", mergedDir).Error("failed to create merged dir")
	}
	didCreateDir = true

	err = unix.Mount("orbstack", mergedDir, "overlay", unix.MS_NOATIME, opts)
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	didMount = true

	fs, err = securefs.NewFromPath(mergedDir)
	if err != nil {
		return fmt.Errorf("open securefs: %w", err)
	}

	logrus.WithField("container_id", ctr.ID).Info("soweli | ================ mounted overlay ================")

	return c.addCertsToFs(fs)
}

func (c *dockerCACertInjector) addCertsToContainer(containerID string) error {
	// check if container is stopped before we try to acquire the in progress status
	ctr, err := c.d.client.InspectContainer(containerID)
	if err != nil {
		return err
	}

	if ctr.State.Status == "running" || ctr.State.Status == "paused" || ctr.State.Status == "restarting" || ctr.State.Status == "removing" {
		logrus.WithFields(logrus.Fields{
			"container_id": ctr.ID,
			"status":       ctr.State.Status,
		}).Debug("container is not in a state to add certs, skipping")
		return nil
	}

	var inProgressWg *sync.WaitGroup
	func() {
		c.inProgressContainersMu.Lock()
		defer c.inProgressContainersMu.Unlock()
		if existingWg, ok := c.inProgressContainers[ctr.ID]; ok {
			inProgressWg = existingWg
			return
		}

		// container is not in progress, make a waitgroup for it
		newWg := &sync.WaitGroup{}
		newWg.Add(1)
		c.inProgressContainers[ctr.ID] = newWg
	}()

	// if we're already trying to add certs to this container, we don't need to do it a second time
	if inProgressWg != nil {
		inProgressWg.Wait()
		return nil
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
	ctr, err = c.d.client.InspectContainer(containerID)
	if err != nil {
		return err
	}

	if ctr.State.Status == "running" || ctr.State.Status == "paused" || ctr.State.Status == "restarting" || ctr.State.Status == "removing" {
		logrus.WithFields(logrus.Fields{
			"container_id": ctr.ID,
			"status":       ctr.State.Status,
		}).Debug("container is not in a state to add certs, skipping")
		// we marked as in progress accidentally, so we need to release it
		c.containerNotInProgress(ctr.ID)
		return nil
	}

	// note, container is marked as not in progress after we get the start event

	return c.addCertsToContainerImpl(ctr)
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
	c.inProgressContainersMu.Lock()
	defer c.inProgressContainersMu.Unlock()
	if wg, ok := c.inProgressContainers[containerID]; ok {
		delete(c.inProgressContainers, containerID)
		wg.Done()
	}
}

func (a *AgentServer) DockerAddCertsToContainer(containerID string, reply *None) error {
	return a.docker.caHackInfo.addCertsToContainer(containerID)
}
