package agent

import (
	"errors"
	"os"
	"regexp"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func WriteHostnameFiles(rootfs string, oldName string, newName string) error {
	readFile := func(path string) (string, error) {
		fPath, err := securejoin.SecureJoin(rootfs, path)
		if err != nil {
			return "", err
		}

		bytes, err := os.ReadFile(fPath)
		if err != nil {
			return "", err
		}

		return string(bytes), nil
	}
	writeFile := func(path string, content string) error {
		fPath, err := securejoin.SecureJoin(rootfs, path)
		if err != nil {
			return err
		}

		err = os.WriteFile(fPath, []byte(content), 0644)
		// a lot of files are read-only on NixOS
		// and user could've also made FS readonly
		if err != nil && !errors.Is(err, unix.EROFS) {
			return err
		}

		return nil
	}

	replaceKvPairs := func(path string, oldName string, newName string) error {
		bytes, err := readFile(path)
		if err != nil {
			// optional
			return nil
		}
		content := string(bytes)

		lines := strings.Split(content, "\n")
		for i, line := range lines {
			k, v, ok := parseShellKvLine(line)
			if !ok {
				continue
			}
			if v == oldName {
				lines[i] = k + "=" + newName
			}
		}

		content = strings.Join(lines, "\n")
		err = writeFile(path, content)
		if err != nil {
			return err
		}

		return nil
	}

	// update /etc/hostname (trailing LF is standard)
	err := writeFile("/etc/hostname", newName+"\n")
	if err != nil {
		return err
	}

	// [all] /etc/hosts (uncommented entries only)
	oldHostRegexp := `(?m)^127\.0\.1\.1\s+` + regexp.QuoteMeta(oldName) + `\s*$`
	hosts, err := readFile("/etc/hosts")
	if err == nil {
		hosts = regexp.MustCompile(oldHostRegexp).ReplaceAllLiteralString(hosts, "127.0.1.1\t"+newName)
		err = writeFile("/etc/hosts", hosts)
		if err != nil {
			return err
		}
	}

	// [NixOS] update lxd.nix
	lxdNix, err := readFile("/etc/nixos/lxd.nix")
	if err == nil {
		lxdNix = strings.ReplaceAll(lxdNix, `networking.hostName = "`+oldName+`";`, `networking.hostName = "`+newName+`";`)
		err = writeFile("/etc/nixos/lxd.nix", lxdNix)
		if err != nil {
			return err
		}

		// now rebuild in background to avoid hanging api
		go func() {
			err := rebuildNixos()
			if err != nil {
				logrus.WithError(err).Error("failed to rebuild nixos for hostname change")
			}
		}()
	}

	// [Rocky, openEuler, Alma] /etc/sysconfig/network-scripts/ifcfg-eth0
	err = replaceKvPairs("/etc/sysconfig/network-scripts/ifcfg-eth0", oldName, newName)
	if err != nil {
		return err
	}

	// [openEuler] /etc/sysconfig/network
	err = replaceKvPairs("/etc/sysconfig/network", oldName, newName)
	if err != nil {
		return err
	}

	// [Gentoo] /etc/conf.d/hostname
	err = replaceKvPairs("/etc/conf.d/hostname", oldName, newName)
	if err != nil {
		return err
	}

	return nil
}

func (a *AgentServer) UpdateHostname(args UpdateHostnameArgs, reply *None) error {
	// two stages:

	// 1. update running system hostname
	// first, try systemd "hostnamectl set-hostname <new>"
	// this also updates /etc/hostname
	err := util.Run("hostnamectl", "set-hostname", args.NewName)
	if err != nil {
		// if that fails, do it the kernel way. we always update /etc/hostname below
		err = unix.Sethostname([]byte(args.NewName))
		if err != nil {
			return err
		}
	}

	// 2. update files
	// common function to be used by scon when container is not running
	err = WriteHostnameFiles("/", args.OldName, args.NewName)
	if err != nil {
		return err
	}

	return nil
}
