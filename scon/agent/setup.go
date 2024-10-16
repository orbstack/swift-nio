package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
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
	defaultUsers = []string{"ubuntu", "archlinux", "opensuse", "alarm"}

	// generally: curl, scp (for Fleet ssh), tar (for vscode ssh)
	PackageInstallCommands = map[string][]string{
		// we really only need scp for JetBrains Fleet, but just install openssh instead of dropbear
		images.ImageAlpine:    {"apk add sudo curl openssh-client-common"},
		images.ImageArch:      {"pacman --noconfirm -Sy openssh", "systemctl disable sshd"},
		images.ImageCentos:    {"dnf install -y tar"},
		images.ImageDebian:    {"apt-get update", "apt-get install -y curl"},
		images.ImageFedora:    nil, // no need
		images.ImageGentoo:    nil, // no need
		images.ImageKali:      {"apt-get update", "apt-get install -y curl"},
		images.ImageOpeneuler: {"dnf install -y tar"},
		images.ImageOpensuse:  {"zypper install -y openssh-clients"},
		images.ImageUbuntu:    {"apt-get update", "apt-get install -y curl"},
		images.ImageVoid:      {"xbps-install -Sy curl"},

		// RHEL distros are missing tar
		images.ImageDevuan: nil, // no need
		images.ImageAlma:   {"dnf install -y tar"},
		//images.ImageAmazon: {"yum install -y curl"},
		images.ImageOracle: {"dnf install -y tar glibc-langpack-en"},
		images.ImageRocky:  {"dnf install -y tar"},

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
	Username string
	Uid      int

	Isolated bool
	// "" if isolated (to avoid info leak)
	HostHomeDir string

	Password          string
	Distro            string
	Version           string
	SSHAuthorizedKeys []string
	Timezone          string
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
		if slices.Contains(addGroups, group) {
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

func deleteDefaultUsers(specifiedUsername string) error {
	for _, username := range defaultUsers {
		if _, err := user.Lookup(username); err != nil {
			continue
		}

		if username == specifiedUsername {
			logrus.WithField("user", username).Debug("Skipping deletion of default user, as this is the username the user wants")
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
	if args.Password == "" {
		// add sudoers.d file if there's no password
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
	}

	// symlink /opt/orbstack-guest/profile
	logrus.Debug("linking profile")
	err := os.Symlink(mounts.ProfileEarly, "/etc/profile.d/000-"+appid.AppName+".sh")
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
	// NOTE: this is a very tiny info leak into isolated machines, but it's fine
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
	_ = os.Remove("/etc/resolv.conf")
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
			// try one more time in case of race w/ dhcp
			// scon_test.go:110: [-32098] create 'itest-2309541203348582721-openeuler-20d03-amd64': setup: do initial setup: symlink /opt/orbstack-guest/etc/resolv.conf /etc/resolv.conf: file exists
			_ = os.Remove("/etc/resolv.conf")
			err = os.Symlink(mounts.ResolvConf, "/etc/resolv.conf")
			if err != nil {
				return err
			}
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

	if !args.Isolated {
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

	// oracle: add repos
	// not in PackageInstallCommands because it depends on version
	if args.Distro == images.ImageOracle {
		logrus.Debug("Installing oracle repos")
		// adds appstream repos and UEK (Unbreakable Enterprise Kernel) repo
		// also dnf modules
		err = util.Run("dnf", "install", "-y", "oraclelinux-release-el"+args.Version, "dnf-plugins-core")
		if err != nil {
			return err
		}

		// could disable/delete UEK but no need
		// dnf config-manager --set-disabled ol8_UEKR6
		// rm -f /etc/yum.repos.d/uek-*.repo (might get reinstalled on update)
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

func (a *AgentServer) createUserAndGroup(username string, uid int, gid int, shell string) error {
	logrus.Debug("Creating group")
	uidStr := strconv.Itoa(uid)
	gidStr := strconv.Itoa(gid)
	groupName := username
	// if it's all numeric, add a prefix. groupadd rejects numeric names
	if _, err := strconv.Atoi(groupName); err == nil {
		groupName = "g" + groupName
	}
	err := util.Run("groupadd", "--gid", gidStr, groupName)
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
	logrus.WithField("user", username).WithField("uid", uid).Debug("Creating user")
	// badname: Void rejects usernames with '.'
	err = util.Run("useradd", "--uid", uidStr, "--gid", gidStr, "--badname", "--no-user-group", "--create-home", "--shell", shell, username)
	if err != nil {
		// older versions of shadow didn't have --badname (Rocky 8, etc.)
		err = util.Run("useradd", "--uid", uidStr, "--gid", gidStr, "--no-user-group", "--create-home", "--shell", shell, username)
		if err != nil {
			// Busybox: add user + user group
			if errors.Is(err, exec.ErrNotFound) {
				err = util.Run("adduser", "-u", uidStr, "-D", "-s", shell, username)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}

	return nil
}

func (a *AgentServer) InitialSetupStage1(args InitialSetupArgs, _ *None) error {
	// if this is a cloud-init image, wait for cloud-init to finish before we do anything
	// fixes errors like: ('ssh_authkey_fingerprints', KeyError("getpwnam(): name not found: 'ubuntu'"))
	err := util.Run("cloud-init", "status", "--wait")
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		logrus.WithError(err).Warn("failed to wait for cloud-init")
	}

	if args.Distro == images.ImageNixos {
		// have to do it all with nix instead
		err = configureSystemNixos(args)
		if err != nil {
			logrus.WithError(err).Error("NixOS system configuration failed")
			return err
		}
	} else {
		// find a shell
		shell, err := selectShell()
		if err != nil {
			return err
		}

		// delete default lxd image users to avoid conflict
		err = deleteDefaultUsers(args.Username)
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
		gid := args.Uid
		err = a.createUserAndGroup(args.Username, args.Uid, gid, shell)
		// ignore if already exists
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}

		// set password
		if args.Password != "" {
			logrus.Debug("Setting password")
			pwdEntries := args.Username + ":" + args.Password + "\nroot:" + args.Password + "\n"
			_, err = util.RunWithInput(pwdEntries, "chpasswd")
			if err != nil {
				return err
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

		// standard system configuration
		err = configureSystemStandard(args)
		if err != nil {
			logrus.WithError(err).Error("standard system configuration failed")
			return err
		}
	}

	return nil
}

func (a *AgentServer) InitialSetupStage2(args InitialSetupArgs, _ *None) error {
	// look up new user info (home, uid, gid)
	u, err := user.Lookup(args.Username)
	if err != nil {
		return err
	}
	home := u.HomeDir
	// in case we're supposed to use an existing user, use that uid/gid
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}

	// write ssh authorized keys
	if len(args.SSHAuthorizedKeys) > 0 {
		logrus.Debug("Writing ssh authorized keys")
		err = os.MkdirAll(home+"/.ssh", 0700)
		if err != nil {
			return err
		}
		err = os.Chown(home+"/.ssh", uid, gid)
		if err != nil {
			return err
		}
		err = os.WriteFile(home+"/.ssh/authorized_keys", []byte(strings.Join(args.SSHAuthorizedKeys, "\n")), 0600)
		if err != nil {
			return err
		}
		err = os.Chown(home+"/.ssh/authorized_keys", uid, gid)
		if err != nil {
			return err
		}
	}

	if !args.Isolated {
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
					err = os.Chown(home+"/.ssh", uid, gid)
					if err != nil {
						return err
					}
					err = os.Symlink(hostHome+"/.ssh/"+sshFile.Name(), home+"/.ssh/"+sshFile.Name())
					if err != nil {
						return err
					}
					err = os.Lchown(home+"/.ssh/"+sshFile.Name(), uid, gid)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// create path translation disambiguation symlink at /mnt/linux
	logrus.Debug("Creating /mnt/linux symlink")
	err = os.Symlink("/", "/mnt/linux")
	if err != nil {
		return err
	}

	return nil
}
