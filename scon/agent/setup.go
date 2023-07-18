package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	adminGroups  = []string{"adm", "wheel", "staff", "admin", "sudo", "video"}
	defaultUsers = []string{"ubuntu", "archlinux", "opensuse"}

	// generally: curl, scp
	PackageInstallCommands = map[string][]string{
		// we really only need scp for JetBrains Fleet, but just install openssh instead of dropbear
		images.ImageAlpine:    {"apk add sudo curl openssh-client-common"},
		images.ImageArch:      {"pacman --noconfirm -Sy openssh", "systemctl disable sshd"},
		images.ImageCentos:    nil, // no need
		images.ImageDebian:    {"apt-get update", "apt-get install -y curl"},
		images.ImageFedora:    nil, // no need
		images.ImageGentoo:    nil, // no need
		images.ImageKali:      {"apt-get update", "apt-get install -y curl"},
		images.ImageOpeneuler: nil, // no need
		images.ImageOpensuse:  {"zypper install -y openssh-clients"},
		images.ImageUbuntu:    {"apt-get update", "apt-get install -y curl"},
		images.ImageVoid:      {"xbps-install -Sy curl"},

		images.ImageDevuan: nil, // no need
		images.ImageAlma:   nil, // no need
		//images.ImageAmazon: {"yum install -y curl"},
		images.ImageOracle: nil, // no need
		images.ImageRocky:  nil, // no need

		// we don't actually use this, but keep it there to force waiting for network
		images.ImageNixos: {"nixos-rebuild switch"}, // using nix instead
	}

	// from arch: rg WatchdogSec= /lib/systemd/system
	systemdServices = []string{"oomd", "resolved", "userdbd", "udevd", "timesyncd", "timedated", "portabled", "nspawn@", "networkd", "machined", "localed", "logind", "journald@", "journald", "journal-remote", "journal-upload", "importd", "hostnamed", "homed"}

	watchdogConf = `[Service]
WatchdogSec=0
`
)

type InitialSetupArgs struct {
	Username    string
	Uid         int
	HostHomeDir string

	Password          string
	Distro            string
	SSHAuthorizedKeys []string
	Timezone          string
	BasicGitConfigs   BasicGitConfigs
}

type BasicGitConfigs struct {
	Email string
	Name  string
}

func selectShell() (string, error) {
	// parse /etc/shells instead of looking up preferred shell in PATH
	// if path we get is not in /etc/shells, e.g. /usr/sbin/bash, then chsh fails
	shells, err := os.ReadFile("/etc/shells")
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(shells), "\n")
	if len(lines) == 0 {
		return "", errors.New("no shells found")
	}

	// find bash
	for _, line := range lines {
		if filepath.Base(line) == "bash" {
			return line, nil
		}
	}

	// then find sh
	for _, line := range lines {
		if filepath.Base(line) == "sh" {
			return line, nil
		}
	}

	// last resort: first line
	return lines[0], nil
}

func selectAdminGroups() []string {
	var groups []string

	for _, group := range adminGroups {
		g, err := user.LookupGroup(group)
		if err != nil {
			continue
		}

		groups = append(groups, g.Name)
	}

	return groups
}

func contains[T comparable](slice []T, item T) bool {
	for _, i := range slice {
		if i == item {
			return true
		}
	}

	return false
}

func addUserToGroupsFile(file string, username string, addGroups []string) error {
	groupsData, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	lines := strings.Split(string(groupsData), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, ":")
		group := parts[0]
		xPasswd := parts[1]
		gid := parts[2]
		members := strings.Split(parts[3], ",")
		if contains(addGroups, group) {
			members = append(members, username)
			lines[i] = fmt.Sprintf("%s:%s:%s:%s", group, xPasswd, gid, strings.Join(members, ","))
		}
	}

	err = os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		return err
	}

	return nil
}

func addUserToGroups(username string, addGroups []string) error {
	err := addUserToGroupsFile("/etc/group", username, addGroups)
	if err != nil {
		return err
	}

	if err := unix.Access("/etc/gshadow", unix.W_OK); err == nil {
		err = addUserToGroupsFile("/etc/gshadow", username, addGroups)
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteDefaultUsers() error {
	for _, username := range defaultUsers {
		if _, err := user.Lookup(username); err != nil {
			continue
		}

		logrus.WithField("user", username).Debug("Deleting default user")
		// Only on GNU distros, so we have userdel
		err := util.Run("userdel", "-r", username)
		if err != nil {
			return err
		}
	}

	return nil
}

func configureSystemStandard(args InitialSetupArgs) error {
	// add sudoers.d file
	logrus.Debug("Adding sudoers.d file")
	sudoersLine := args.Username + " ALL=(ALL) NOPASSWD:ALL"
	err := os.MkdirAll("/etc/sudoers.d", 0750)
	if err != nil {
		return err
	}
	err = os.WriteFile("/etc/sudoers.d/orbstack", []byte(sudoersLine), 0440)
	if err != nil {
		return err
	}

	// symlink /opt/orbstack-guest/profile
	logrus.Debug("linking profile")
	err = os.Symlink(mounts.ProfileEarly, "/etc/profile.d/000-"+appid.AppName+".sh")
	if err != nil {
		return err
	}
	err = os.Symlink(mounts.ProfileLate, "/etc/profile.d/999-"+appid.AppName+".sh")
	if err != nil {
		return err
	}

	// set timezone
	// don't use systemd timedatectl. it can change the system clock sometimes (+8h for pst)
	// glibc will reload eventually so it's ok
	logrus.WithField("timezone", args.Timezone).Debug("Setting timezone")

	os.Remove("/etc/localtime")
	err = os.Symlink("/usr/share/zoneinfo/"+args.Timezone, "/etc/localtime")
	if err != nil {
		return err
	}

	// disable systemd-resolved if running (arch, debian, ubuntu, fedora)
	// it's redundant because we have mac's mDNSResponder
	// and breaks single-name unicast ("andromeda") and unicast .local ("andromeda.local")
	if _, err := os.Stat("/run/systemd/resolve/io.systemd.Resolve"); err == nil {
		logrus.Debug("disabling systemd-resolved")
		err = util.Run("systemctl", "disable", "systemd-resolved")
		if err != nil {
			logrus.WithError(err).Warn("Failed to disable systemd-resolved")
		}
		err = util.Run("systemctl", "stop", "systemd-resolved")
		if err != nil {
			logrus.WithError(err).Warn("Failed to stop systemd-resolved")
		}
		// mask it to be safe
		err = util.Run("systemctl", "mask", "systemd-resolved")
		if err != nil {
			logrus.WithError(err).Warn("Failed to stop systemd-resolved")
		}
	}

	// link resolv.conf
	// we do this for all distros just in case there's a race condition where resolv.conf isn't set up yet at this point
	// because we install packages below, and that requires network
	logrus.Debug("linking resolv.conf")
	err = os.Remove("/etc/resolv.conf")
	if err != nil {
		return err
	}
	// Kali doesn't like this, networking fails to start
	if args.Distro == images.ImageKali {
		resolvConf, err := os.ReadFile(mounts.ResolvConf)
		if err != nil {
			return err
		}
		err = os.WriteFile("/etc/resolv.conf", resolvConf, 0644)
		if err != nil {
			return err
		}
	} else {
		err = os.Symlink(mounts.ResolvConf, "/etc/resolv.conf")
		if err != nil {
			return err
		}
	}

	// disable systemd service watchdogs to save CPU (wakes up every 2-3 min)
	// only for systemd distros
	if _, err := exec.LookPath("systemctl"); err == nil {
		logrus.Debug("disabling systemd service watchdogs")
		for _, service := range systemdServices {
			overrideDir := "/etc/systemd/system/systemd-" + service + ".service.d"
			err = os.MkdirAll(overrideDir, 0755)
			if err != nil {
				return err
			}
			overrideConf := overrideDir + "/override.conf"
			err = os.WriteFile(overrideConf, []byte(watchdogConf), 0644)
			if err != nil {
				return err
			}
		}

		// won't take effect until next boot, that's ok
		err = util.Run("systemctl", "daemon-reload")
		if err != nil {
			logrus.WithError(err).Warn("Failed to reload systemd")
		}
	}

	// link extra certs
	logrus.Debug("linking extra certificates")
	if _, err := exec.LookPath("update-ca-certificates"); err == nil {
		// debian scripts
		logrus.Debug("using update-ca-certificates")
		err = os.MkdirAll("/usr/local/share/ca-certificates", 0755)
		if err != nil {
			return err
		}
		err = os.Symlink(mounts.ExtraCerts, "/usr/local/share/ca-certificates/orbstack-extra-certs.crt")
		if err != nil {
			return err
		}
		err = util.Run("update-ca-certificates")
		if err != nil {
			return err
		}
	} else if _, err := exec.LookPath("update-ca-trust"); err == nil {
		// p11-kit
		logrus.Debug("using update-ca-trust")
		// arch /etc/ca-certificates/trust-source/anchors
		if _, err := os.Stat("/etc/ca-certificates/trust-source/anchors"); err == nil {
			err = os.Symlink(mounts.ExtraCerts, "/etc/ca-certificates/trust-source/anchors/orbstack-extra-certs.crt")
			if err != nil {
				return err
			}
		} else if _, err := os.Stat("/etc/pki/ca-trust/source/anchors"); err == nil {
			err = os.Symlink(mounts.ExtraCerts, "/etc/pki/ca-trust/source/anchors/orbstack-extra-certs.crt")
			if err != nil {
				return err
			}
		} else {
			return errors.New("no trust-source/anchors directory found")
		}

		err = util.Run("update-ca-trust")
		if err != nil {
			return err
		}
	}

	// install packages
	pkgCommands, ok := PackageInstallCommands[args.Distro]
	if ok && len(pkgCommands) > 0 {
		for _, cmd := range pkgCommands {
			args := strings.Split(cmd, " ")
			logrus.WithField("args", args).Debug("Running package install command")
			err = util.Run(args...)
			if err != nil {
				return err
			}
		}
	}

	// symlink /etc/ssh/ssh_config.d/10-orbstack
	// after we've installed openssh packages if necessary
	if _, err := os.Stat("/etc/ssh/ssh_config.d"); errors.Is(err, os.ErrNotExist) {
		logrus.Debug("creating ssh_config.d")
		err = os.Mkdir("/etc/ssh/ssh_config.d", 0755)
		if err == nil {
			// add to ssh_config
			oldConfig, err := os.ReadFile("/etc/ssh/ssh_config")
			if err == nil {
				newConfig := "Include /etc/ssh/ssh_config.d/*.conf\n\n" + string(oldConfig)
				err = os.WriteFile("/etc/ssh/ssh_config", []byte(newConfig), 0644)
				if err != nil {
					return err
				}
			} else {
				logrus.WithError(err).Warn("Failed to read ssh_config")
			}
		} else {
			logrus.WithError(err).Warn("Failed to create ssh_config.d")
		}
	}
	logrus.Debug("linking ssh config")
	err = os.Symlink(mounts.SshConfig, "/etc/ssh/ssh_config.d/10-"+appid.AppName+".conf")
	if err != nil {
		// error ok, not all distros have ssh_config.d
		// this isn't *that* important
		logrus.WithError(err).Warn("Failed to symlink ssh config")
	}

	return nil
}

func (a *AgentServer) InitialSetup(args InitialSetupArgs, _ *None) error {
	// find a shell
	shell, err := selectShell()
	if err != nil {
		return err
	}

	// delete default lxd image users to avoid conflict
	err = deleteDefaultUsers()
	if err != nil {
		return err
	}

	// Alpine: install shadow early for standard "useradd" tool. Busybox checks uid <= 256000
	if args.Distro == images.ImageAlpine {
		// keep cache for other pkg installation
		err = util.Run("apk", "add", "shadow")
		if err != nil {
			return err
		}
	}

	// create user group
	logrus.Debug("Creating group")
	uidStr := strconv.Itoa(args.Uid)
	gid := args.Uid
	gidStr := strconv.Itoa(gid)
	groupName := args.Username
	// if it's all numeric, add a prefix. groupadd rejects numeric names
	if _, err := strconv.Atoi(groupName); err == nil {
		groupName = "g" + groupName
	}
	err = util.Run("groupadd", "--gid", gidStr, groupName)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			// Busybox: this is ok, do nothing.
			// Busybox adduser already creates user group with matching GID.
		} else {
			return err
		}
	}

	// create user
	// uid = host, gid = 1000+
	logrus.WithField("user", args.Username).WithField("uid", args.Uid).Debug("Creating user")
	// badname: Void rejects usernames with '.'
	err = util.Run("useradd", "--uid", uidStr, "--gid", gidStr, "--badname", "--no-user-group", "--create-home", "--shell", shell, args.Username)
	if err != nil {
		// Busybox: add user + user group
		if errors.Is(err, exec.ErrNotFound) {
			err = util.Run("adduser", "-u", uidStr, "-D", "-s", shell, args.Username)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// set password
	if args.Password != "" {
		logrus.Debug("Setting password")
		pwdEntries := args.Username + ":" + args.Password + "\nroot:" + args.Password + "\n"
		err = util.RunWithInput(pwdEntries, "chpasswd")
		if err != nil {
			return err
		}
	}

	// look up new home
	u, err := user.Lookup(args.Username)
	if err != nil {
		return err
	}
	home := u.HomeDir

	// write ssh authorized keys
	if len(args.SSHAuthorizedKeys) > 0 {
		logrus.Debug("Writing ssh authorized keys")
		err = os.MkdirAll(home+"/.ssh", 0700)
		if err != nil {
			return err
		}
		err = os.Chown(home+"/.ssh", args.Uid, gid)
		if err != nil {
			return err
		}
		err = os.WriteFile(home+"/.ssh/authorized_keys", []byte(strings.Join(args.SSHAuthorizedKeys, "\n")), 0600)
		if err != nil {
			return err
		}
		err = os.Chown(home+"/.ssh/authorized_keys", args.Uid, gid)
		if err != nil {
			return err
		}
	}

	// write gitconfig
	if args.BasicGitConfigs.Email != "" && args.BasicGitConfigs.Name != "" {
		logrus.Debug("Writing gitconfig")
		gitConfig := fmt.Sprintf(`# Basic config generated from your macOS config by %s.
# Feel free to edit it or symlink /Users/%s/.gitconfig instead.

[user]
	name = %s
	email = %s
`, appid.UserAppName, args.Username, args.BasicGitConfigs.Name, args.BasicGitConfigs.Email)
		err = os.WriteFile(home+"/.gitconfig", []byte(gitConfig), 0644)
		if err != nil {
			return err
		}

		// chown
		err = os.Chown(home+"/.gitconfig", args.Uid, gid)
		if err != nil {
			return err
		}
	}

	// symlink id_* ssh keys in case user has encrypted keys w/o agent
	logrus.Debug("Symlinking ssh keys")
	hostHome := mounts.Virtiofs + args.HostHomeDir
	sshFiles, err := os.ReadDir(hostHome + "/.ssh")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		for _, sshFile := range sshFiles {
			if strings.HasPrefix(sshFile.Name(), "id_") {
				logrus.WithField("file", sshFile.Name()).Debug("Symlinking ssh key")
				err = os.MkdirAll(home+"/.ssh", 0700)
				if err != nil {
					return err
				}
				err = os.Chown(home+"/.ssh", args.Uid, gid)
				if err != nil {
					return err
				}
				err = os.Symlink(hostHome+"/.ssh/"+sshFile.Name(), home+"/.ssh/"+sshFile.Name())
				if err != nil {
					return err
				}
				err = os.Chown(home+"/.ssh/"+sshFile.Name(), args.Uid, gid)
				if err != nil {
					return err
				}
			}
		}
	}

	// add user to admin groups
	// Alpine has no usermod, so we have to do this manually
	groups := selectAdminGroups()
	logrus.WithField("groups", groups).Debug("Adding user to groups")
	err = addUserToGroups(args.Username, groups)
	if err != nil {
		return err
	}

	// create path translation disambiguation symlink at /mnt/linux
	logrus.Debug("Creating /mnt/linux symlink")
	err = os.Symlink("/", "/mnt/linux")
	if err != nil {
		return err
	}

	/*
	 * after this point is system configs
	 */
	if args.Distro == images.ImageNixos {
		// have to do it all with nix instead
		err = configureSystemNixos(args)
		if err != nil {
			logrus.WithError(err).Error("NixOS system configuration failed")
			return err
		}
	} else {
		// standard system configuration
		err = configureSystemStandard(args)
		if err != nil {
			logrus.WithError(err).Error("standard system configuration failed")
			return err
		}
	}

	return nil
}
