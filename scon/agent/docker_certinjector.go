package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/orbstack/macvirt/scon/securefs"
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

	caCertLabelFalseValues = []string{"false", "0", "off"}
)

type dockerContainerConfigV2 struct {
	Config struct {
		Labels map[string]string
	}
}

type ociConfig struct {
	Root struct {
		Path     string `json:"path"`
		Readonly bool   `json:"readonly"`
	} `json:"root"`
}

type dockerCACertInjector struct {
	rootCertPem []byte
}

func newDockerCACertInjector(d *DockerAgent) *dockerCACertInjector {
	rootCertPem, err := d.host.GetTLSRootData()
	if err != nil {
		logrus.WithError(err).Error("failed to get root cert data")
	}

	return &dockerCACertInjector{
		rootCertPem: []byte(rootCertPem.CertPEM),
	}
}

func writeFileIfChanged(fs *securefs.FS, path string, data []byte, perm os.FileMode) error {
	existing, err := fs.ReadFile(path)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	if bytes.Equal(existing, data) {
		return nil
	}
	return fs.WriteFile(path, data, perm)
}

func (c *dockerCACertInjector) addToFS(fs *securefs.FS) error {
	for _, dirPath := range caCertDirs {
		// just attempt to write as if the dir exists
		// ENOTDIR = parent exists but is a file, not dir
		// ENOENT = parent dir does not exist
		targetPath := dirPath + "/" + rootCaCertName
		err := writeFileIfChanged(fs, targetPath, c.rootCertPem, 0o644)
		if err != nil {
			if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) {
				logrus.WithField("dirPath", dirPath).Debug("cert injector: dir does not exist, skipping")
			} else {
				logrus.WithError(err).WithField("dirPath", dirPath).Error("cert injector: failed to write to dir, skipping")
			}
			continue
		}
	}

	for _, filePath := range caCertFiles {
		file, err := fs.OpenFile(filePath, unix.O_RDWR|unix.O_APPEND, 0)
		if err != nil {
			if !errors.Is(err, unix.ENOENT) {
				logrus.WithError(err).WithField("filePath", filePath).Debug("cert injector: failed to open file")
			}
			continue
		}
		defer file.Close()

		contents, err := io.ReadAll(file)
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Debug("cert injector: failed to read file, skipping")
			continue
		}

		if bytes.Contains(contents, c.rootCertPem) {
			logrus.WithField("filePath", filePath).Debug("cert injector: file already contains root cert, skipping")
			continue
		}

		// add extra \n in case last cert didn't end with one
		// also add a trailing \n in case some other script adds a cert later
		_, err = file.Write([]byte("\n" + string(c.rootCertPem) + "\n"))
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Error("cert injector: failed to write to file, skipping")
			continue
		}
	}

	for conditionPath, targetDirPath := range conditionalCaCertDirs {
		if _, err := fs.Stat(conditionPath); err != nil {
			logrus.WithError(err).WithField("conditionPath", conditionPath).Debug("cert injector: condition path does not exist, skipping")
			continue
		}

		err := fs.MkdirAll(targetDirPath, 0o755)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("cert injector: failed to create target dir, skipping")
			continue
		}

		err = writeFileIfChanged(fs, targetDirPath+"/"+rootCaCertName, c.rootCertPem, 0o644)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("cert injector: failed to write to target dir, skipping")
			continue
		}
	}

	return nil
}

func (c *dockerCACertInjector) addToContainer(containerID string) error {
	// read labels: if dev.orbstack.add-ca-certificates=false, then skip injection
	configJson, err := os.ReadFile("/var/lib/docker/containers/" + containerID + "/config.v2.json")
	if err != nil {
		return fmt.Errorf("read container config: %w", err)
	}

	var config dockerContainerConfigV2
	err = json.Unmarshal(configJson, &config)
	if err != nil {
		return fmt.Errorf("unmarshal container config: %w", err)
	}

	if slices.Contains(caCertLabelFalseValues, config.Config.Labels["dev.orbstack.add-ca-certificates"]) {
		return nil
	}

	// read OCI config from containerd to get mounts
	ociConfigJson, err := os.ReadFile("/var/run/docker/containerd/daemon/io.containerd.runtime.v2.task/moby/" + containerID + "/config.json")
	if err != nil {
		return fmt.Errorf("read OCI config: %w", err)
	}

	var ociConfig ociConfig
	err = json.Unmarshal(ociConfigJson, &ociConfig)
	if err != nil {
		return fmt.Errorf("unmarshal OCI config: %w", err)
	}

	// we could check ociConfig.Root.Readonly, but users are likely to expect that read-only containers still get certs injected; read-only is a runtime state for security in prod, and it's helpful to simulate that for parity in dev, but why should that affect the state from *before* container code starts running?
	// the only possible side-effect of that is unexpected changes showing up if the user `commit`s a read-only container, but that action doesn't make sense in the first place

	if ociConfig.Root.Path == "" {
		return errors.New("missing root path in OCI config")
	}

	// usually /var/lib/docker/overlay2/.../merged, but this works with containerd snapshotter too
	fs, err := securefs.NewFromPath(ociConfig.Root.Path)
	if err != nil {
		return fmt.Errorf("create securefs: %w", err)
	}
	defer fs.Close()

	return c.addToFS(fs)
}

func (a *AgentServer) DockerAddCertsToContainer(containerID string, reply *None) error {
	err := a.docker.certInjector.addToContainer(containerID)
	if err != nil {
		return err
	}
	return nil
}
