package agent

import (
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

func replaceKvPairs(path string, oldName string, newName string) error {
	bytes, err := os.ReadFile(path)
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
	err = os.WriteFile(path, []byte(content), 0644)
	// in case it's read-only (NixOS or some other weird user setup)
	if err != nil && !errors.Is(err, unix.EROFS) {
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

	// update /etc/hostname (trailing LF is standard)
	err = os.WriteFile("/etc/hostname", []byte(args.NewName+"\n"), 0644)
	// on NixOS this is read-only
	if err != nil && !errors.Is(err, unix.EROFS) {
		return err
	}

	// [all] /etc/hosts (uncommented entries only)
	oldHostRegexp := `(?m)^127\.0\.1\.1\s+` + regexp.QuoteMeta(args.OldName) + `\s*$`
	hostsBytes, err := os.ReadFile("/etc/hosts")
	if err == nil {
		hosts := string(hostsBytes)
		hosts = regexp.MustCompile(oldHostRegexp).ReplaceAllLiteralString(hosts, "127.0.1.1")
		err = os.WriteFile("/etc/hosts", []byte(hosts), 0644)
		// on NixOS this is read-only
		if err != nil && !errors.Is(err, unix.EROFS) {
			return err
		}
	}

	// [NixOS] update lxd.nix
	lxdNixBytes, err := os.ReadFile("/etc/nixos/lxd.nix")
	if err == nil {
		lxdNix := string(lxdNixBytes)
		lxdNix = strings.ReplaceAll(lxdNix, `networking.hostName = "`+args.OldName+`";`, `networking.hostName = "`+args.NewName+`";`)
		err = os.WriteFile("/etc/nixos/lxd.nix", []byte(lxdNix), 0644)
		if err != nil {
			return err
		}
	}

	// [Rocky, openEuler, Alma] /etc/sysconfig/network-scripts/ifcfg-eth0
	err = replaceKvPairs("/etc/sysconfig/network-scripts/ifcfg-eth0", args.OldName, args.NewName)
	if err != nil {
		return err
	}

	// [openEuler] /etc/sysconfig/network
	err = replaceKvPairs("/etc/sysconfig/network", args.OldName, args.NewName)
	if err != nil {
		return err
	}

	// [Gentoo] /etc/conf.d/hostname
	err = replaceKvPairs("/etc/conf.d/hostname", args.OldName, args.NewName)
	if err != nil {
		return err
	}

	return nil
}
