package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	adminGroups  = []string{"adm", "wheel", "staff", "admin", "sudo", "video"}
	defaultUsers = []string{"ubuntu", "archlinux", "opensuse"}

	// generally: curl, scp
	PackageInstallCommands = map[string][]string{
		images.ImageAlpine:   {"apk add sudo curl dropbear-scp"},
		images.ImageArch:     {"pacman --noconfirm -Sy dropbear-scp"},
		images.ImageCentos:   nil, // no need
		images.ImageDebian:   {"apt-get update", "apt-get install -y curl"},
		images.ImageFedora:   nil, // no need
		images.ImageGentoo:   nil, // no need
		images.ImageKali:     {"apt-get update", "apt-get install -y curl"},
		images.ImageOpensuse: {"zypper install -y openssh-clients"},
		images.ImageUbuntu:   {"apt-get update", "apt-get install -y curl"},
		images.ImageVoid:     {"xbps-install -Sy curl"},

		images.ImageDevuan: nil, // no need
		images.ImageAlma:   nil, // no need
		//images.ImageAmazon: {"yum install -y curl"},
		images.ImageOracle: nil, // no need
		images.ImageRocky:  nil, // no need
	}
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
	if shell, err := exec.LookPath("bash"); err == nil {
		return shell, nil
	}

	if shell, err := exec.LookPath("sh"); err == nil {
		return shell, nil
	}

	// first line of /etc/shells
	shells, err := os.ReadFile("/etc/shells")
	if err != nil {
		return "", err
	}

	shell := strings.Split(string(shells), "\n")[0]
	return shell, nil
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

func (a *AgentServer) InitialSetup(args InitialSetupArgs, _ *None) error {
	// find a shell
	shell, err := selectShell()
	if err != nil {
		return err
	}

	// delete default users to avoid conflict
	err = deleteDefaultUsers()
	if err != nil {
		return err
	}

	// create user group
	logrus.Debug("Creating group")
	uidStr := strconv.Itoa(args.Uid)
	err = util.Run("groupadd", "--gid", uidStr, args.Username)
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
	err = util.Run("useradd", "--uid", uidStr, "--gid", uidStr, "--no-user-group", "--create-home", "--shell", shell, args.Username)
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
		err = os.Chown(home+"/.ssh", args.Uid, args.Uid)
		if err != nil {
			return err
		}
		err = os.WriteFile(home+"/.ssh/authorized_keys", []byte(strings.Join(args.SSHAuthorizedKeys, "\n")), 0600)
		if err != nil {
			return err
		}
		err = os.Chown(home+"/.ssh/authorized_keys", args.Uid, args.Uid)
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
		err = os.Chown(home+"/.gitconfig", args.Uid, args.Uid)
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
				err = os.Chown(home+"/.ssh", args.Uid, args.Uid)
				if err != nil {
					return err
				}
				err = os.Symlink(hostHome+"/.ssh/"+sshFile.Name(), home+"/.ssh/"+sshFile.Name())
				if err != nil {
					return err
				}
				err = os.Chown(home+"/.ssh/"+sshFile.Name(), args.Uid, args.Uid)
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

	// add sudoers.d file
	logrus.Debug("Adding sudoers.d file")
	sudoersLine := args.Username + " ALL=(ALL) NOPASSWD:ALL"
	err = os.MkdirAll("/etc/sudoers.d", 0750)
	if err != nil {
		return err
	}
	err = os.WriteFile("/etc/sudoers.d/"+args.Username, []byte(sudoersLine), 0440)
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

	// symlink /etc/ssh/ssh_config.d/10-orbstack
	logrus.Debug("linking ssh config")
	err = os.Symlink(mounts.SshConfig, "/etc/ssh/ssh_config.d/10-"+appid.AppName+".conf")
	if err != nil {
		// error ok, not all distros have ssh_config.d
		// this isn't *that* important
		logrus.WithError(err).Warn("Failed to symlink ssh config")
	}

	// set timezone
	if args.Timezone != "" {
		// don't use systemd timedatectl. it can change the system clock sometimes (+8h for pst)
		logrus.WithField("timezone", args.Timezone).Debug("Setting timezone")

		os.Remove("/etc/localtime")
		err = os.Symlink("/usr/share/zoneinfo/"+args.Timezone, "/etc/localtime")
		if err != nil {
			return err
		}
	}

	// disable systemd-resolved if running (arch, debian, ubuntu, fedora)
	// it's redundant because we have mac's mDNSResponder
	// and breaks single-name unicast ("andromeda") and unicast .local ("andromeda.local")
	if _, err := os.Stat("/run/systemd/resolve/io.systemd.Resolve"); err == nil {
		logrus.Debug("Disabling systemd-resolved")
		err = util.Run("systemctl", "disable", "systemd-resolved")
		if err != nil {
			logrus.WithError(err).Warn("Failed to disable systemd-resolved")
		}
		err = util.Run("systemctl", "stop", "systemd-resolved")
		if err != nil {
			logrus.WithError(err).Warn("Failed to stop systemd-resolved")
		}
	}

	// link resolv.conf
	// we do this for all distros just in case there's a race condition where resolv.conf isn't set up yet at this point
	// because we install packages below, and that requires network
	logrus.Debug("Symlinking resolv.conf")
	err = os.Remove("/etc/resolv.conf")
	if err != nil {
		return err
	}
	err = os.Symlink(mounts.ResolvConf, "/etc/resolv.conf")
	if err != nil {
		return err
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

	return nil
}
