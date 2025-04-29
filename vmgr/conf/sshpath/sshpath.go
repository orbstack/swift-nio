package sshpath

import (
	"os"
	"path"
	"strings"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

type PathTranslatorFunc[T any] func(string, T) string

type ToMacOptions struct {
	NfsRoot        string
	NfsMachineRoot string
	Relaxed        bool
}

type ToLinuxOptions struct {
	// optional
	TargetContainer string
}

func ToMac(p string, opts ToMacOptions) string {
	// canonicalize first
	p = path.Clean(p)

	// if path is under mac virtiofs mount, remove the mount prefix
	if p == mounts.Virtiofs {
		return "/"
	} else if strings.HasPrefix(p, mounts.Virtiofs+"/") {
		return strings.TrimPrefix(p, mounts.Virtiofs)
	}

	// if path is under machines mount, remove the mount prefix
	if p == mounts.Machines {
		// /mnt/machines -> /Users/<user>/OrbStack -- is basically the same
		return opts.NfsRoot
	} else if machineAndPath := strings.TrimPrefix(p, mounts.Machines+"/"); machineAndPath != p {
		// trimmed = machine name + remainder. we can just append that directly to the nfs root path because it follows the same structure
		return opts.NfsRoot + "/" + machineAndPath
	}

	// nothing to do for linked paths
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// translate explicit /mnt/linux prefix for disambiguation
	if p == mounts.LinuxExplicit || strings.HasPrefix(p, mounts.LinuxExplicit+"/") {
		return opts.NfsMachineRoot + strings.TrimPrefix(p, mounts.LinuxExplicit)
	}

	// otherwise...
	if opts.Relaxed {
		// if relaxed: only translate home (/home/<user>)
		linuxHome, err := os.UserHomeDir()
		if err != nil {
			panic(err)
		}

		if p == linuxHome || strings.HasPrefix(p, linuxHome+"/") {
			return opts.NfsMachineRoot + p
		}
	} else {
		// if aggressive: translate everything
		return opts.NfsMachineRoot + p
	}

	return p
}

func ToLinux(p string, opts ToLinuxOptions) string {
	// canonicalize first
	p = path.Clean(p)

	// is it relative? if so, translate it to absolute
	if !path.IsAbs(p) {
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		p = path.Join(cwd, p)
	}

	// if we kow the container, then we can translate from NFS mountpoint
	if opts.TargetContainer != "" {
		containerNfsPrefix := coredir.NfsMountpoint() + "/" + opts.TargetContainer
		if p == containerNfsPrefix || strings.HasPrefix(p, containerNfsPrefix+"/") {
			return p[len(containerNfsPrefix):]
		}
	}

	// common case: is it linked?
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// nope, needs translation
	return mounts.Virtiofs + p
}
