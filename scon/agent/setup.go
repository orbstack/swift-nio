package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	vmconf "github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
)

var (
	adminGroups = []string{"wheel", "staff", "admin", "sudo"}
)

type InitialSetupArgs struct {
	Username string
	Uid      int
	Password string
}

func run(combinedArgs ...string) error {
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s; output: %s", err, string(output))
	}

	return nil
}

func runWithInput(input string, combinedArgs ...string) error {
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s; output: %s", err, string(output))
	}

	return nil
}

func selectShell() (string, error) {
	shell := "/bin/bash"
	if _, err := exec.LookPath(shell); err == nil {
		return shell, nil
	}

	shell = "/bin/sh"
	if _, err := exec.LookPath(shell); err == nil {
		return shell, nil
	}

	// first line of /etc/shells
	shells, err := os.ReadFile("/etc/shells")
	if err != nil {
		return "", err
	}

	shell = strings.Split(string(shells), "\n")[0]
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

func addUserToGroups(username string, addGroups []string) error {
	groupsData, err := os.ReadFile("/etc/group")
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

	err = os.WriteFile("/etc/group", []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		return err
	}

	return nil
}

func (a *AgentServer) InitialSetup(args InitialSetupArgs, _ *None) error {
	// find a shell
	shell, err := selectShell()
	if err != nil {
		return err
	}

	// create user
	// uid = host, gid = 1000+
	err = run("useradd", "-u", strconv.Itoa(args.Uid), "-m", "-s", shell, args.Username)
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		// Busybox: add user + user group
		err = run("adduser", "-u", strconv.Itoa(args.Uid), "-D", "-s", shell, args.Username)
		if err != nil {
			return err
		}
	}

	// set password
	if args.Password != "" {
		pwdEntry := args.Username + ":" + args.Password
		err = runWithInput(pwdEntry, "chpasswd")
		if err != nil {
			return err
		}
	}

	// add user to admin groups
	// Alpine has no usermod, so we have to do this manually
	groups := selectAdminGroups()
	err = addUserToGroups(args.Username, groups)
	if err != nil {
		return err
	}

	// symlink /opt/macvirt-guest/profile
	err = os.Symlink(mounts.ProfileEarly, "/etc/profile.d/000-"+vmconf.AppName()+".sh")
	if err != nil {
		return err
	}
	err = os.Symlink(mounts.ProfileLate, "/etc/profile.d/999-"+vmconf.AppName()+".sh")
	if err != nil {
		return err
	}

	return nil
}
