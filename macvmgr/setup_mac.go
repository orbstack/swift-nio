package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alessio/shellescape"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/guihelper"
	"github.com/kdrag0n/macvirt/macvmgr/setup/userutil"
	"github.com/kdrag0n/macvirt/macvmgr/syssetup"
	"github.com/kdrag0n/macvirt/macvmgr/util"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient/vmtypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

const (
	gidAdmin = 80
)

const (
	dataReadmeText = `# OrbStack data

This folder is used to store all OrbStack data, including Docker images, containers, and Linux machines.

If you see an 8 TB data.img file, don't panic! It's a special type of file that it only takes as much space as you use, and automatically shrinks when you delete data. It does *not* take up 8 TB of disk space.

To find the real size:
    - Run "du -sh data.img" in Terminal, or
    - Right-click the file and select "Get Info"
      Then look at "size on disk"

For more details, see https://docs.orbstack.dev/readme-link/data-img
`
)

type UserDetails struct {
	IsAdmin bool
	Shell   string
	Path    string
	Home    string
}

type PathInfo struct {
	Dir          string
	RequiresRoot bool
}

var (
	binCommands   = []string{"orbctl", "orb"}
	xbinCommands  = []string{"docker", "docker-buildx", "docker-compose", "docker-credential-osxkeychain"}
	dockerPlugins = []string{"docker-buildx", "docker-compose"}
	// consider: docker-buildx hub-tool docker-index
)

func parseShellLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func getUserDetails() (*UserDetails, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	gids, err := u.GroupIds()
	if err != nil {
		return nil, err
	}
	// check if admin
	isAdmin := false
	gidAdminStr := strconv.Itoa(gidAdmin)
	for _, gid := range gids {
		// querying group info can take a long time with active directory
		// so just check for gid 80
		if gid == gidAdminStr {
			isAdmin = true
			break
		}
	}

	// look up the user's shell
	// until Go os/user supports shell, use cgo getpwuid_r instead of dscl
	// dscl returns exit status 70 or killed sometimes
	shell, err := userutil.GetShell()
	if err != nil {
		return nil, err
	}

	// look up the user's PATH
	// run login shell first to get profile
	// then run sh in case of fish
	// force -i (interactive) in case user put PATH in .zshrc/.bashrc
	// use single quotes to avoid expansion in zsh
	// nu shell doesn't like combining short args (-lic) so split them
	out, err := util.RunLoginShell(shell, "-i", "-c", `sh -c 'echo "$PATH"'`)
	if err != nil {
		return nil, err
	}
	logrus.WithField("path", out).WithField("shell", shell).Debug("user path")
	path := parseShellLine(out)

	return &UserDetails{
		IsAdmin: isAdmin,
		Shell:   shell,
		Path:    path,
		Home:    u.HomeDir,
	}, nil
}

// we're started under launchd with only this PATH: /usr/bin:/bin:/usr/sbin:/sbin
func setupPath() error {
	details, err := getUserDetails()
	if err != nil {
		return err
	}

	os.Setenv("PATH", details.Path)
	return nil
}

/*
1. ~/bin IF exists AND in path (or ~/.local/bin)
2. /usr/local/bin IF root
3. ZDOTDIR/zprofile or profile IF is default shell + set AlertProfileChanged
4. ask user + set AlertRequestAddPath
*/
func findTargetCmdPath(details *UserDetails, pathItems []string) (*PathInfo, error) {
	home := details.Home

	// prefer ~/bin because user probably created it explicitly
	_, err := os.Stat(home + "/bin")
	if err == nil {
		// is it in path?
		if slices.Contains(pathItems, home+"/bin") {
			return &PathInfo{
				Dir:          home + "/bin",
				RequiresRoot: false,
			}, nil
		}
	}

	// ~/.local/bin is a common alternative
	_, err = os.Stat(home + "/.local/bin")
	if err == nil {
		// is it in path?
		if slices.Contains(pathItems, home+"/.local/bin") {
			return &PathInfo{
				Dir:          home + "/.local/bin",
				RequiresRoot: false,
			}, nil
		}
	}

	// check if root
	if details.IsAdmin {
		return &PathInfo{
			Dir:          "/usr/local/bin",
			RequiresRoot: true,
		}, nil
	}

	// fall back to our path
	return nil, nil
}

func fixDockerCredsStore() error {
	dockerConfigPath := conf.UserDockerDir() + "/config.json"
	if _, err := os.Stat(dockerConfigPath); err == nil {
		// read the file
		data, err := os.ReadFile(dockerConfigPath)
		if err != nil {
			return err
		}

		// parse it
		var config map[string]interface{}
		err = json.Unmarshal(data, &config)
		if err != nil {
			return err
		}

		// check if it's set to desktop
		if config["credsStore"] == "desktop" {
			// does it exist? if so, keep it
			if _, err := exec.LookPath("docker-credential-desktop"); err == nil {
				logrus.Debug("docker-credential-desktop exists, keeping credsStore=desktop")
			} else {
				// otherwise, change it
				logrus.Debug("docker-credential-desktop doesn't exist, changing credsStore to osxkeychain")
				// change it to osxkeychain
				config["credsStore"] = "osxkeychain"

				// write it back
				data, err := json.MarshalIndent(config, "", "  ")
				if err != nil {
					return err
				}
				err = os.WriteFile(dockerConfigPath, data, 0644)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func filterSlice[T comparable](s []T, f func(T) bool) []T {
	var out []T
	for _, v := range s {
		if f(v) {
			out = append(out, v)
		}
	}
	return out
}

func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	m := d.Mode()
	if m.IsDir() {
		return unix.EISDIR
	}
	err = unix.ENOSYS
	// ENOSYS means Eaccess is not available or not implemented.
	// EPERM can be returned by Linux containers employing seccomp.
	// In both cases, fall back to checking the permission bits.
	if err == nil || (err != unix.ENOSYS && err != unix.EPERM) {
		return err
	}
	if m&0111 != 0 {
		return nil
	}
	return fs.ErrPermission
}

// takes custom PATH
func execLookPath(path string, file string) (string, error) {
	// NOTE(rsc): I wish we could use the Plan 9 behavior here
	// (only bypass the path if file begins with / or ./ or ../)
	// but that would not match all the Unix shells.

	if strings.Contains(file, "/") {
		err := findExecutable(file)
		if err == nil {
			return file, nil
		}
		return "", fmt.Errorf("find %s: %w", file, err)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell semantics: path element "" means "."
			dir = "."
		}
		path := filepath.Join(dir, file)
		if err := findExecutable(path); err == nil {
			if !filepath.IsAbs(path) {
				return path, fmt.Errorf("ErrDot: %s", path)
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("ErrNotFound: %s", file)
}

// XXX: xbin is special - relink if:
// - is from any .app (Docker Desktop, or ourself)
// - target doesn't exist
// so we don't replace homebrew or nix one
func lookPathXbinRelink(pathEnv string, cmd string) (string, error) {
	path, err := execLookPath(pathEnv, cmd)
	if err != nil {
		return "", err
	}
	// resolve link dest
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}

	// cond 1: if .app
	if strings.Contains(path, ".app/") {
		return "", fmt.Errorf("is in app: %s", path)
	}
	// cond 2: if target doesn't exist
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("target doesn't exist: %s", path)
	}

	return path, nil
}

func shouldUpdateSymlink(src, dest string) bool {
	currentSrc, err := os.Readlink(dest)
	if err == nil && currentSrc == src {
		// already linked
		return false
	}

	return true
}

func symlinkIfNotExists(src, dest string) error {
	if !shouldUpdateSymlink(src, dest) {
		return nil
	}

	// link it
	os.Remove(dest)
	err := os.Symlink(src, dest)
	if err != nil {
		return err
	}

	return nil
}

func linkToAppBin(src string) error {
	dest := conf.UserAppBinDir() + "/" + filepath.Base(src)
	return symlinkIfNotExists(src, dest)
}

func writeShellProfileSnippets() error {
	shells := conf.ShellInitDir()
	bin := conf.UserAppBinDir()

	bashSnippet := fmt.Sprintf(`export PATH=%s:"$PATH"`+"\n", shellescape.Quote(bin))
	err := os.WriteFile(shells+"/init.bash", []byte(bashSnippet), 0644)
	if err != nil {
		return err
	}

	zshSnippet := bashSnippet
	err = os.WriteFile(shells+"/init.zsh", []byte(zshSnippet), 0644)
	if err != nil {
		return err
	}

	fishSnippet := fmt.Sprintf(`set -gxp PATH %s`+"\n", shellescape.Quote(bin))
	err = os.WriteFile(shells+"/init.fish", []byte(fishSnippet), 0644)
	if err != nil {
		return err
	}

	return nil
}

func writeDataReadme() error {
	// write readme
	return os.WriteFile(conf.DataDir()+"/README.txt", []byte(dataReadmeText), 0644)
}

func readDockerConfigEnv(shell string) error {
	out, err := util.RunLoginShell(shell, "-i", "-c", `sh -c 'echo "$DOCKER_CONFIG"'`)
	if err != nil {
		return err
	}
	logrus.WithField("DOCKER_CONFIG", out).Debug("user DOCKER_CONFIG")
	value := parseShellLine(out)
	if value != "" && strings.HasPrefix(value, "/") {
		os.Setenv("DOCKER_CONFIG", value)
	}

	return nil
}

/*
for commands:
1. ~/bin IF exists AND in path (or ~/.local/bin)
2. /usr/local/bin IF root
3. ZDOTDIR/zprofile or profile IF is default shell + set AlertProfileChanged
4. ask user + set AlertRequestAddPath

for docker sock:
/var/run/docker.sock IF root
*/
func (s *VmControlServer) doHostSetup() (*vmtypes.SetupInfo, error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	if s.setupDone {
		return &vmtypes.SetupInfo{}, nil
	}

	details, err := getUserDetails()
	if err != nil {
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"admin": details.IsAdmin,
		"shell": details.Shell,
		"path":  details.Path,
		"home":  details.Home,
	}).Debug("user details")
	// split path
	pathItems := strings.Split(details.Path, ":")

	// link docker sock?
	var adminCommands []string
	adminLinkDocker := false
	adminLinkCommands := false
	if details.IsAdmin {
		sockDest, err := os.Readlink("/var/run/docker.sock")
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// doesn't exist - ignore, relink
			} else if errors.Is(err, syscall.EINVAL) {
				// not a link - ignore, relink
			} else {
				return nil, err
			}
		}
		wantDest := conf.DockerSocket()
		if sockDest != wantDest {
			logrus.Debug("[request admin] link docker socket")
			adminCommands = append(adminCommands, "rm -f /var/run/docker.sock; ln -sf "+wantDest+" /var/run/docker.sock")
			adminLinkDocker = true
		}
	}

	// figure out where we want to put commands
	targetCmdPath, err := findTargetCmdPath(details, pathItems)
	if err != nil {
		return nil, err
	}
	if targetCmdPath == nil {
		logrus.Debug("target command path is nil")
	} else {
		logrus.WithFields(logrus.Fields{
			"dir":          targetCmdPath.Dir,
			"requiresRoot": targetCmdPath.RequiresRoot,
		}).Debug("target command path")
	}

	// first, always put them in ~/.orbstack/bin
	// check each bin command
	for _, cmd := range binCommands {
		cmdSrc := conf.CliBinDir() + "/" + cmd
		err = linkToAppBin(cmdSrc)
		if err != nil {
			return nil, err
		}
	}

	// check each xbin command
	for _, cmd := range xbinCommands {
		cmdSrc := conf.CliXbinDir() + "/" + cmd
		err = linkToAppBin(cmdSrc)
		if err != nil {
			return nil, err
		}
	}

	// write all the shell profile snippets
	err = writeShellProfileSnippets()
	if err != nil {
		return nil, err
	}

	err = writeDataReadme()
	if err != nil {
		return nil, err
	}

	// always try to add to profile and/or ask user to add to $PATH
	var askAddPath bool
	var alertProfileChangedPath *string
	// if there's no existing (home/system) path to link to, we *require* a shell $PATH
	shellPathRequired := targetCmdPath == nil
	// is the PATH already there?
	if !slices.Contains(pathItems, conf.CliBinDir()) && !slices.Contains(pathItems, conf.CliXbinDir()) && !slices.Contains(pathItems, conf.UserAppBinDir()) {
		// do we recognize this shell?
		shellBase := filepath.Base(details.Shell)
		var profilePath string
		var initSnippetPath string
		switch shellBase {
		case "zsh":
			// what's the ZDOTDIR?
			// no need for -i (interactive), ZDOTDIR must be in .zshenv
			out, err := util.RunLoginShell(details.Shell, "-c", `echo "$ZDOTDIR"`)
			if err != nil {
				return nil, err
			}
			zdotdir := parseShellLine(out)
			if zdotdir == "" {
				zdotdir = details.Home
			}
			profilePath = zdotdir + "/.zprofile"
			initSnippetPath = conf.ShellInitDir() + "/init.zsh"
			fallthrough
		case "bash":
			if profilePath == "" {
				profilePath = details.Home + "/.profile"
				initSnippetPath = conf.ShellInitDir() + "/init.bash"
			}

			// common logic for bash and zsh
			profileData, err := os.ReadFile(profilePath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// we'll create it
					profileData = []byte{}
				} else {
					return nil, err
				}
			}
			profileScript := string(profileData)

			// is it already there?
			// no quote: need ~/ to stay intact
			// we only check for the base (source %s) because:
			//   - 2>/dev/null was added later
			//   - user can edit it
			lineBase := fmt.Sprintf(`source %s`, syssetup.MakeHomeRelative(initSnippetPath))
			line := fmt.Sprintf(`%s 2>/dev/null || :`, lineBase)
			logrus.WithFields(logrus.Fields{
				"shell":    shellBase,
				"file":     profilePath,
				"lineBase": lineBase,
			}).Debug("checking for lineBase in profile")
			if !strings.Contains(profileScript, lineBase) {
				// if not, add it
				profileScript += fmt.Sprintf("\n# Added by %s: command-line tools and integration\n%s\n", appid.UserAppName, line)
				err = os.WriteFile(profilePath, []byte(profileScript), 0644)
				if err != nil {
					// if profile is read-only, e.g. with nix home-manager
					logrus.WithError(err).WithFields(logrus.Fields{
						"shell": shellBase,
						"file":  profilePath,
					}).Warn("failed to write shell profile")
				} else {
					// success
					// not important enough to nag user if we can link to an existing path
					if shellPathRequired {
						relProfilePath := syssetup.MakeHomeRelative(profilePath)
						alertProfileChangedPath = &relProfilePath
					}
					logrus.Debug("modified profile")
				}
			}
		default:
			// we don't know how to deal with this.
			// just ask the user to add it to their path
			if shellPathRequired {
				logrus.Debug("unknown shell, asking user to add to path")
				askAddPath = true
			}
			// if shell path isn't required, it's ok, let it slide. not important
		}
	}

	// do we need to add to $PATH?
	if targetCmdPath != nil {
		doLinkCommand := func(src string) error {
			dest := targetCmdPath.Dir + "/" + filepath.Base(src)

			// skip if link update is not needed
			if !shouldUpdateSymlink(src, dest) {
				logrus.WithFields(logrus.Fields{
					"src": src,
					"dst": dest,
				}).Debug("skipping link (no update needed)")
				return nil
			}

			// if root isn't required, just link it
			if !targetCmdPath.RequiresRoot {
				logrus.WithFields(logrus.Fields{
					"src": src,
					"dst": dest,
				}).Debug("linking command (as user)")
				err = symlinkIfNotExists(src, dest)
				if err != nil {
					return err
				}
				return nil
			}

			// otherwise, we need to add it to adminCommands and set the flag
			logrus.WithFields(logrus.Fields{
				"src": src,
				"dst": dest,
			}).Debug("[request admin] linking command (as root)")
			adminCommands = append(adminCommands, shellescape.QuoteCommand([]string{"ln", "-sf", src, dest}))
			adminLinkCommands = true
			return nil
		}

		// check each bin command
		// to avoid a feedback loop, we ignore our own ~/.orbstack/bin when checking this
		otherPathItems := filterSlice(pathItems, func(item string) bool {
			return item != conf.UserAppBinDir()
		})
		otherPathEnv := strings.Join(otherPathItems, ":")
		for _, cmd := range binCommands {
			cmdSrc := conf.CliBinDir() + "/" + cmd
			logrus.WithFields(logrus.Fields{
				"cmd": cmd,
				"src": cmdSrc,
			}).Debug("checking bin command")

			// always link our bin
			err = doLinkCommand(cmdSrc)
			if err != nil {
				return nil, err
			}
		}

		// check each xbin command
		for _, cmd := range xbinCommands {
			_, err := lookPathXbinRelink(otherPathEnv, cmd)
			cmdSrc := conf.CliXbinDir() + "/" + cmd
			logrus.WithFields(logrus.Fields{
				"cmd": cmd,
				"src": cmdSrc,
			}).Debug("checking xbin command")

			// relink conditions are in lookPathXbinRelink
			if err != nil {
				// link it
				err := doLinkCommand(cmdSrc)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// get DOCKER_CONFIG
	// below is first usage of it
	err = readDockerConfigEnv(details.Shell)
	if err != nil {
		logrus.WithError(err).Warn("failed to read DOCKER_CONFIG env")
	}

	// link docker CLI plugins
	dockerPluginsDir := conf.DockerCliPluginsDir()
	for _, plugin := range dockerPlugins {
		pluginSrc := conf.CliXbinDir() + "/" + plugin
		pluginDest := dockerPluginsDir + "/" + plugin
		logrus.WithFields(logrus.Fields{
			"src": pluginSrc,
			"dst": pluginDest,
		}).Debug("linking docker CLI plugin")
		err = symlinkIfNotExists(pluginSrc, pluginDest)
		if err != nil {
			return nil, err
		}

		// TODO: /usr/local/lib/docker/cli-plugins
	}

	// need to fix docker creds store?
	// in case user uninstalled Docker Desktop, we need to change it
	// we don't consider this critical, so ignore errors
	err = fixDockerCredsStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to fix docker creds store")
	}

	info := &vmtypes.SetupInfo{}
	if len(adminCommands) > 0 {
		// join and escape commands
		cmd := "set -e; " + strings.Join(adminCommands, "; ")
		info.AdminShellCommand = &cmd

		var adminReasons []string
		if adminLinkCommands {
			adminReasons = append(adminReasons, "install command-line tools")
		}
		if adminLinkDocker {
			adminReasons = append(adminReasons, "improve Docker socket compatibility")
		}
		msg := strings.Join(adminReasons, " and ")
		info.AdminMessage = &msg
	}
	if askAddPath {
		info.AlertRequestAddPaths = []string{
			conf.UserAppBinDir(),
		}
	}
	if alertProfileChangedPath != nil {
		info.AlertProfileChanged = alertProfileChangedPath
	}
	logrus.WithFields(logrus.Fields{
		"adminShellCommand": strp(info.AdminShellCommand),
		"adminMessage":      strp(info.AdminMessage),
		"alertAddPaths":     info.AlertRequestAddPaths,
		"alertProfile":      info.AlertProfileChanged,
	}).Debug("prepare setup info done")

	s.setupDone = true
	return info, nil
}

// for CLI-only, this completes the setup without GUI
func completeSetupCli(info *vmtypes.SetupInfo) error {
	// notify profile changed
	if info.AlertProfileChanged != nil {
		logrus.WithField("profile", *info.AlertProfileChanged).Info("notifying profile changed")
		profileRelPath := *info.AlertProfileChanged
		err := guihelper.Notify(guihelper.Notification{
			Title:   "Shell Profile Changed",
			Message: "Command-line tools added to PATH. To use them, run: source " + profileRelPath,
		})
		if err != nil {
			return err
		}
	}

	// notify add paths
	if info.AlertRequestAddPaths != nil {
		logrus.WithField("paths", info.AlertRequestAddPaths).Info("notifying add paths")
		paths := strings.Join(info.AlertRequestAddPaths, " and ")
		err := guihelper.Notify(guihelper.Notification{
			Title:   "Add Tools to PATH",
			Message: "To use command-line tools, add " + paths + " to your PATH.",
		})
		if err != nil {
			return err
		}
	}

	// request run as admin
	if info.AdminShellCommand != nil {
		logrus.WithField("cmd", *info.AdminShellCommand).Debug("requesting run as admin")
		prompt := ""
		if info.AdminMessage != nil {
			prompt = *info.AdminMessage
		}

		err := guihelper.RunAsAdmin(*info.AdminShellCommand, "OrbStack wants to "+prompt+". This is optional.")
		if err != nil {
			return err
		}
	}

	return nil
}

func strp(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
