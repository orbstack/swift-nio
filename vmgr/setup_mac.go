package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alessio/shellescape"
	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/dockerconf"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/setup/userutil"
	"github.com/orbstack/macvirt/vmgr/syssetup"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vzf"
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
	Home    string

	EnvPATH          string
	EnvDOCKER_CONFIG string
	EnvZDOTDIR       string
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

func (s *VmControlServer) doGetUserDetails() (*UserDetails, error) {
	logrus.Info("reading user account info")
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	// get current process supplementary groups to avoid querying server for network accounts
	gids, err := unix.Getgroups()
	if err != nil {
		return nil, err
	}
	// check if admin
	isAdmin := slices.Contains(gids, gidAdmin)

	// look up the user's shell
	// until Go os/user supports shell, use cgo getpwuid_r instead of dscl
	// dscl returns exit status 70 or killed sometimes
	shell, err := userutil.GetShell()
	if err != nil {
		return nil, err
	}

	// look up the user's PATH and other environment vars
	// run login shell first to get profile
	// then run sh in case of fish
	// force -i (interactive) in case user put PATH in .zshrc/.bashrc
	// use single quotes to avoid expansion in zsh
	// nu shell doesn't like combining short args (-lic) so split them
	logrus.Info("reading user environment variables")
	envReport, err := s.runEnvReport(shell, "-i")
	if err != nil {
		logrus.WithError(err).Warn("failed to read user environment variables, retrying without interactive")
		envReport, err = s.runEnvReport(shell)
		if err != nil {
			// proceed with empty report
			logrus.WithError(err).Error("failed to read user environment variables w/o interactive")
			envReport = &vmtypes.EnvReport{
				Environ: []string{"PATH=" + os.Getenv("PATH")},
			}
		}
	}

	envMap := envutil.ToMap(envReport.Environ)

	return &UserDetails{
		IsAdmin: isAdmin,
		Shell:   shell,
		Home:    u.HomeDir,

		EnvPATH:          envMap["PATH"],
		EnvDOCKER_CONFIG: envMap["DOCKER_CONFIG"],
		EnvZDOTDIR:       envMap["ZDOTDIR"],
	}, nil
}

// we're started under launchd with only this PATH: /usr/bin:/bin:/usr/sbin:/sbin
func (s *VmControlServer) doGetUserDetailsAndSetupEnv() (*UserDetails, error) {
	details, err := s.doGetUserDetails()
	if err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"admin": details.IsAdmin,
		"shell": details.Shell,
		"home":  details.Home,

		"envPATH":          details.EnvPATH,
		"envDOCKER_CONFIG": details.EnvDOCKER_CONFIG,
		"envZDOTDIR":       details.EnvZDOTDIR,
	}).Debug("user details")

	err = os.Setenv("PATH", details.EnvPATH)
	if err != nil {
		return nil, err
	}

	// also set DOCKER_CONFIG
	err = setDockerConfigEnv(details.EnvDOCKER_CONFIG)
	if err != nil {
		logrus.WithError(err).Warn("failed to set DOCKER_CONFIG env")
	}

	return details, nil
}

func (s *VmControlServer) getUserDetailsAndSetupEnv() (*UserDetails, error) {
	result := s.setupUserDetailsOnce.Do(func() Result[*UserDetails] {
		details, err := s.doGetUserDetailsAndSetupEnv()
		return Result[*UserDetails]{details, err}
	})
	return result.Value, result.Err
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
	// MOD: change return value
	/* // never true with mod
	err = unix.ENOSYS
	// ENOSYS means Eaccess is not available or not implemented.
	// EPERM can be returned by Linux containers employing seccomp.
	// In both cases, fall back to checking the permission bits.
	if err == nil || (err != unix.ENOSYS && err != unix.EPERM) {
		return err
	}
	*/
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
	logrus.WithField("dest", dest).Info("symlinking")
	_ = os.Remove(dest)
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

func setDockerConfigEnv(value string) error {
	if value != "" && strings.HasPrefix(value, "/") {
		logrus.WithField("path", value).Info("detected DOCKER_CONFIG")

		// let's try to ensure the dir
		_, err := coredir.EnsureDir(value)
		if err != nil {
			// it doesn't work - doesn't exist and we couldn't create.
			// fall back to ~/.docker
			logrus.WithError(err).WithField("path", value).Warn("invalid DOCKER_CONFIG dir")
			value = ""
		}

		err = os.Setenv("DOCKER_CONFIG", value)
		if err != nil {
			return err
		}
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
func (s *VmControlServer) doHostSetup() (retSetup *vmtypes.SetupInfo, retErr error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()

	if s.setupDone {
		return &vmtypes.SetupInfo{}, nil
	}

	// too many risks of panic in here
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	details, err := s.getUserDetailsAndSetupEnv()
	if err != nil {
		return nil, err
	}
	// split path
	pathItems := strings.Split(details.EnvPATH, ":")

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
			logrus.Info("[request admin] link docker socket")
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
		logrus.Info("no target command path, using ~/.orbstack/bin")
	} else {
		logrus.WithFields(logrus.Fields{
			"dir":          targetCmdPath.Dir,
			"requiresRoot": targetCmdPath.RequiresRoot,
		}).Info("target command path")
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
			zdotdir := details.EnvZDOTDIR
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
					logrus.Info("modified shell profile")
				}
			}
		default:
			// we don't know how to deal with this.
			// just ask the user to add it to their path
			if shellPathRequired {
				logrus.Info("unknown shell, asking user to add bins to PATH")
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
			}).Info("[request admin] linking command (as root)")
			adminCommands = append(adminCommands,
				shellescape.QuoteCommand([]string{"mkdir", "-p", filepath.Dir(dest)}),
				shellescape.QuoteCommand([]string{"ln", "-sf", src, dest}))
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
	err = dockerconf.FixDockerCredsStore()
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
	logrus.Info("setup done")
	return info, nil
}

// for CLI-only, this completes the setup without GUI
func completeSetupCli(info *vmtypes.SetupInfo) error {
	// notify profile changed
	if info.AlertProfileChanged != nil {
		logrus.WithField("profile", *info.AlertProfileChanged).Info("notifying profile changed")
		profileRelPath := *info.AlertProfileChanged
		err := guihelper.Notify(guitypes.Notification{
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
		err := guihelper.Notify(guitypes.Notification{
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

		err := vzf.SwextGuiRunAsAdmin(*info.AdminShellCommand, "OrbStack wants to "+prompt+". This is optional.")
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
