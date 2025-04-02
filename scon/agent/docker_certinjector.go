package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const rootCaCertName = "orbstack-root.crt"
const maxPemSize = 128 * 1024 * 1024 // 128 MiB

var (
	// from Go crypto/tls
	caCertDirs = []string{
		"/etc/ssl/certs",
		"/usr/local/share/certs",
		"/etc/pki/tls/certs",
		"/etc/openssl/certs",
		"/var/ssl/certs",
	}
	// x: y means that directory y is created if path x exists, and the root cert is written to it
	// allows us to prime certs for the ca-certificates package's postinstall bundle creation script, without polluting distroless containers
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
	pems    [][]byte
	allPems []byte
}

func newDockerCACertInjector(d *DockerAgent) *dockerCACertInjector {
	extraCerts, err := d.host.GetExtraCaCertificates()
	if err != nil {
		logrus.WithError(err).Error("failed to get extra certs")
		extraCerts = nil
	}

	byteCerts := make([][]byte, len(extraCerts))
	for i, cert := range extraCerts {
		byteCerts[i] = []byte(cert)
	}

	return &dockerCACertInjector{
		pems:    byteCerts,
		allPems: []byte(strings.Join(extraCerts, "\n")),
	}
}

func writeFileIfChanged(fs *securefs.FS, path string, data []byte, perm os.FileMode) (_written bool, _err error) {
	file, err := fs.OpenFile(path, unix.O_RDWR|unix.O_CREAT, perm)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, nil
		} else {
			return false, fmt.Errorf("open: %w", err)
		}
	}
	defer file.Close()

	existing, err := io.ReadAll(io.LimitReader(file, maxPemSize))
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	if bytes.Equal(existing, data) {
		return false, nil
	}

	// write/rewrite
	err = file.Truncate(0)
	if err != nil {
		return false, fmt.Errorf("truncate: %w", err)
	}

	_, err = file.Write(data)
	if err != nil {
		return false, fmt.Errorf("write: %w", err)
	}

	return true, nil
}

func (c *dockerCACertInjector) addAllToFile(fs *securefs.FS, path string) error {
	file, err := fs.OpenFile(path, unix.O_RDWR|unix.O_APPEND, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		} else {
			return fmt.Errorf("open: %w", err)
		}
	}
	defer file.Close()

	// limit read size to prevent OOM DoS
	contents, err := io.ReadAll(io.LimitReader(file, maxPemSize))
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	for _, pem := range c.pems {
		if bytes.Contains(contents, pem) {
			// TODO: also skip rest of the container here?
			// alpine symlinks /etc/ssl/cert.pem -> /etc/ssl/certs/ca-certificates.crt, so we add the cert the first time around, and think it's duplicate when we hit the symlink
			// O_NOFOLLOW deals with that but seems like it could be a bit flaky
			return nil
		}

		// add extra \n in case last cert didn't end with one
		// also add a trailing \n in case some other script adds a cert later
		_, err = file.Write([]byte("\n" + string(pem) + "\n"))
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}
	}

	return nil
}

func (c *dockerCACertInjector) addToFS(fs *securefs.FS) error {
	for _, dirPath := range caCertDirs {
		// just attempt to write as if the dir exists
		// ENOTDIR = parent exists but is a file, not dir
		// ENOENT = parent dir does not exist
		targetPath := dirPath + "/" + rootCaCertName
		// O_EXCL write would be faster and simpler, but this is more robust if data.img is migrated to a different host, but keychain isn't, so the new host has a different CA
		written, err := writeFileIfChanged(fs, targetPath, c.allPems, 0o644)
		if err != nil {
			if !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ENOTDIR) {
				logrus.WithError(err).WithField("dirPath", dirPath).Error("cert injector: failed to write to dir, skipping")
			}
			continue
		}

		if written {
			logrus.WithField("dirPath", dirPath).Debug("cert injector: added to dir")
		} else {
			// existing + matching cert means that we've already added stuff to this container
			// for perf, skip all other checks
			logrus.WithField("dirPath", dirPath).Debug("cert injector: file already exists, skipping container")
			return nil
		}
	}

	for _, filePath := range caCertFiles {
		err := c.addAllToFile(fs, filePath)
		if err != nil {
			logrus.WithError(err).WithField("filePath", filePath).Error("cert injector: failed to add to file, skipping")
		}
	}

	for conditionPath, targetDirPath := range conditionalCaCertDirs {
		if _, err := fs.Stat(conditionPath); err != nil {
			if !errors.Is(err, unix.ENOENT) {
				logrus.WithError(err).WithField("conditionPath", conditionPath).Debug("cert injector: failed to stat condition path")
			}
			continue
		}

		err := fs.MkdirAll(targetDirPath, 0o755)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("cert injector: failed to create dir, skipping")
			continue
		}

		written, err := writeFileIfChanged(fs, targetDirPath+"/"+rootCaCertName, c.allPems, 0o644)
		if err != nil {
			logrus.WithError(err).WithField("targetDirPath", targetDirPath).Error("cert injector: failed to write to dir, skipping")
			continue
		}

		if written {
			logrus.WithField("targetDirPath", targetDirPath).Debug("cert injector: added to conditional dir")
		} else {
			logrus.WithField("targetDirPath", targetDirPath).Debug("cert injector: file already exists, skipping container")
			return nil
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
