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
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	adminGroups  = []string{"adm", "wheel", "staff", "admin", "sudo", "video"}
	defaultUsers = []string{"ubuntu", "archlinux", "opensuse"}
)

type InitialSetupArgs struct {
	Username          string
	Uid               int
	Password          string
	Distro            string
	SSHAuthorizedKeys []string
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

	// create user
	// uid = host, gid = 1000+
	logrus.WithField("user", args.Username).WithField("uid", args.Uid).Debug("Creating user")
	err = util.Run("useradd", "-u", strconv.Itoa(args.Uid), "-m", "-s", shell, args.Username)
	if err != nil {
		// Busybox: add user + user group
		if errors.Is(err, exec.ErrNotFound) {
			err = util.Run("adduser", "-u", strconv.Itoa(args.Uid), "-D", "-s", shell, args.Username)
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

	// write ssh authorized keys
	if len(args.SSHAuthorizedKeys) > 0 {
		logrus.Debug("Writing ssh authorized keys")
		home := "/home/" + args.Username
		err = os.MkdirAll(home+"/.ssh", 0700)
		if err != nil {
			return err
		}
		err = os.WriteFile(home+"/.ssh/authorized_keys", []byte(strings.Join(args.SSHAuthorizedKeys, "\n")), 0600)
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

	// symlink /opt/macvirt-guest/profile
	logrus.Debug("linking profile")
	err = os.Symlink(mounts.ProfileEarly, "/etc/profile.d/000-"+appid.AppName+".sh")
	if err != nil {
		return err
	}
	err = os.Symlink(mounts.ProfileLate, "/etc/profile.d/999-"+appid.AppName+".sh")
	if err != nil {
		return err
	}

	// create mac symlinks
	logrus.Debug("Creating mac symlinks")
	for _, path := range mounts.LinkedPaths {
		err = os.Symlink(mounts.VirtiofsMountpoint+path, path)
		if err != nil {
			return err
		}
	}
	err = os.Symlink(mounts.VirtiofsMountpoint, "/mac")
	if err != nil {
		return err
	}

	// Alpine: install sudo - we have no root password
	if args.Distro == "alpine" {
		logrus.Debug("Installing sudo")
		err = util.Run("apk", "add", "sudo")
		if err != nil {
			return err
		}
	}

	return nil
}
