package vmgr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/dockerconf"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/setup/userutil"
	"github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/syssetup"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	dataReadmeText = `# OrbStack data

This folder is used to store all OrbStack data, including Docker images, containers, and Linux machines.

If you see an 8 TB data.img file, don't panic! It's a special type of file that it only takes as much space as you use, and automatically shrinks when you delete data. It does *not* take up 8 TB of disk space.

To find the real size:
    - Run "du -sh data.img" in Terminal, or
    - Right-click the file and select "Get Info"
      Then look at "size on disk"

For more details, see https://orbsta.cc/data-img
`
)

var (
	binCommands = []string{"orbctl", "orb"}
	// some people run "docker-compose"
	xbinCommands  = []string{"docker", "docker-compose", "docker-credential-osxkeychain", "kubectl"}
	dockerPlugins = []string{"docker-buildx", "docker-compose"}
)

type UserDetails struct {
	Shell string
	Home  string

	EnvPATH          string
	EnvDOCKER_CONFIG string
	EnvZDOTDIR       string
	EnvSSH_AUTH_SOCK string
}

func (s *VmControlServer) doGetUserDetails(useAdmin bool) (*UserDetails, error) {
	logrus.Debug("reading user account info")
	u, err := user.Current()
	if err != nil {
		return nil, err
	}

	// uninstall priv helper if admin is disabled but available
	if vmconfig.IsAdmin() && !useAdmin {
		err := swext.PrivhelperUninstall()
		if err != nil {
			logrus.WithError(err).Error("failed to uninstall priv helper")
		}
	}

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
		Shell: shell,
		Home:  u.HomeDir,

		EnvPATH:          envMap["PATH"],
		EnvDOCKER_CONFIG: envMap["DOCKER_CONFIG"],
		EnvZDOTDIR:       envMap["ZDOTDIR"],
		EnvSSH_AUTH_SOCK: envMap["SSH_AUTH_SOCK"],
	}, nil
}

// we're started under launchd with only this PATH: /usr/bin:/bin:/usr/sbin:/sbin
func (s *VmControlServer) doGetUserDetailsAndSetupEnv() (*UserDetails, error) {
	useAdmin := vmconfig.Get().Setup_UseAdmin
	details, err := s.doGetUserDetails(useAdmin)
	if err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"admin": useAdmin,
		"shell": details.Shell,
		"home":  details.Home,

		"envPATH":          details.EnvPATH,
		"envDOCKER_CONFIG": details.EnvDOCKER_CONFIG,
		"envZDOTDIR":       details.EnvZDOTDIR,
		"envSSH_AUTH_SOCK": details.EnvSSH_AUTH_SOCK,
	}).Debug("user details")

	err = os.Setenv("PATH", details.EnvPATH)
	if err != nil {
		return nil, err
	}
	err = os.Setenv("SSH_AUTH_SOCK", details.EnvSSH_AUTH_SOCK)
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

func writeFileIfChanged(path string, data []byte, perm os.FileMode) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Equal(existing, data) {
		return nil
	}
	return os.WriteFile(path, data, perm)
}

func symlinkIfChanged(src, dst string) error {
	oldSrc, err := os.Readlink(dst)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// ignore: we'll create it
		} else if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTDIR) {
			// not a symlink: probably placed manually by user, so don't overwrite
			// ENOTDIR: some users have file at ~/.local or ~/.local/bin
			return nil
		} else {
			return err
		}
	}

	if oldSrc != src {
		_ = os.Remove(dst)
		return os.Symlink(src, dst)
	}

	return nil
}

func symlinkExistingAppIfChanged(src, dst string) error {
	oldSrc, err := os.Readlink(dst)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTDIR) {
			// not a symlink or doesn't exist, don't overwrite
			// ENOTDIR: some users have file at ~/.local or ~/.local/bin
			return nil
		} else {
			return err
		}
	}

	if oldSrc == src || !strings.Contains(oldSrc, ".app/") {
		return nil
	}

	_ = os.Remove(dst)
	return os.Symlink(src, dst)
}

func shouldSymlinkApp(src, dst string) (bool, error) {
	oldSrc, err := os.Readlink(dst)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// not found: we'll create it
			return true, nil
		} else if errors.Is(err, unix.EINVAL) {
			// not a symlink: probably placed manually by user, so don't overwrite
			return false, nil
		} else {
			return false, err
		}
	}

	// different from symlinkIfChanged: only relink if oldSrc is to .app, so we don't override homebrew
	return oldSrc != src && strings.Contains(oldSrc, ".app/"), nil
}

func writeShellProfileSnippets() error {
	shells := conf.ShellInitDir()
	bin := conf.UserAppBinDir()

	// append, not prepend.
	// cmdlinks should probably be prepended but it causes issues with kubectl/docker/etc. overrides
	bashSnippetBase := fmt.Sprintf(`export PATH="$PATH":%s`+"\n", shellescape.Quote(bin))
	// completions don't work with old macOS bash 3.2, and also require bash-completion
	bashSnippet := bashSnippetBase
	err := writeFileIfChanged(shells+"/init.bash", []byte(bashSnippet), 0644)
	if err != nil {
		return err
	}

	// zsh loads completions from fpath, but this must be set *before* compinit
	// people usually put compinit in .zshrc, and init.zsh should be included in .zprofile, so it should work
	zshSnippet := bashSnippetBase + "\nfpath+=" + shellescape.Quote(conf.CliZshCompletionsDir())
	err = writeFileIfChanged(shells+"/init.zsh", []byte(zshSnippet), 0644)
	if err != nil {
		return err
	}

	// due to a bug in v1.10.1 where init.fish was accidentally added to .zprofile,
	// we need to use init2.fish and delete init.fish. running init.fish enables xtrace in zsh (set -x),
	// which breaks things horribly.
	fishSnippet := fmt.Sprintf(`set -gxa PATH %s`+"\n", shellescape.Quote(bin))
	_ = os.Remove(shells + "/init.fish")
	err = writeFileIfChanged(shells+"/init2.fish", []byte(fishSnippet), 0644)
	if err != nil {
		return err
	}

	// install fish completions if ~/.config/fish exists
	if err := unix.Access(conf.FishCompletionsDir(), unix.W_OK); err == nil {
		// clear broken symlink but leave files alone
		if err := unix.Access(conf.FishCompletionsDir()+"/docker.fish", unix.F_OK); errors.Is(err, unix.ENOENT) {
			_ = os.Remove(conf.FishCompletionsDir() + "/docker.fish")
		}
		err = symlinkIfChanged(conf.CliCompletionsDir()+"/docker.fish", conf.FishCompletionsDir()+"/docker.fish")
		if err != nil {
			return err
		}

		// clear broken symlink but leave files alone
		if err := unix.Access(conf.FishCompletionsDir()+"/kubectl.fish", unix.F_OK); errors.Is(err, unix.ENOENT) {
			_ = os.Remove(conf.FishCompletionsDir() + "/kubectl.fish")
		}
		err = symlinkIfChanged(conf.CliCompletionsDir()+"/kubectl.fish", conf.FishCompletionsDir()+"/kubectl.fish")
		if err != nil {
			return err
		}
	}

	return nil
}

func writeDataReadme() error {
	// write readme
	return writeFileIfChanged(conf.DataDir()+"/README.txt", []byte(dataReadmeText), 0644)
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

type shellProfileEditResult struct {
	AlertAddPath        bool
	AlertProfileChanged bool
}

func editShellProfile(shellBase string, profilePath string, initSnippetPath string) (shellProfileEditResult, error) {
	// common logic for bash, zsh, & fish
	profileData, err := os.ReadFile(profilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// we'll create it
			profileData = []byte{}
		} else {
			return shellProfileEditResult{}, err
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
		profileScript += fmt.Sprintf("\n# Added by %s: command-line tools and integration\n# This won't be added again if you remove it.\n%s\n", appid.UserAppName, line)
		err = os.WriteFile(profilePath, []byte(profileScript), 0644)
		if err != nil {
			// if profile is read-only, e.g. with nix home-manager
			logrus.WithError(err).WithFields(logrus.Fields{
				"shell": shellBase,
				"file":  profilePath,
			}).Warn("failed to write shell profile")
			return shellProfileEditResult{
				AlertAddPath: true,
			}, nil
		} else {
			// success
			// not important enough to nag user if we can link to an existing path
			logrus.Info("modified shell profile")

			// record that we have added it
			err = vmconfig.UpdateState(func(state *vmconfig.VmgrState) error {
				state.SetupState.EditedShellProfiles = append(state.SetupState.EditedShellProfiles, shellBase)
				return nil
			})
			if err != nil {
				return shellProfileEditResult{}, err
			}

			return shellProfileEditResult{
				AlertProfileChanged: true,
			}, nil
		}
	}

	return shellProfileEditResult{}, nil
}

func (s *VmControlServer) tryModifyShellProfile(details *UserDetails, pathItems []string) (shellProfileEditResult, error) {
	// always try to add to profile and/or ask user to add to $PATH
	// is the PATH already there?
	if slices.Contains(pathItems, conf.CliBinDir()) || slices.Contains(pathItems, conf.CliXbinDir()) || slices.Contains(pathItems, conf.UserAppBinDir()) {
		return shellProfileEditResult{}, nil
	}

	// do we recognize this shell?
	shellBase := filepath.Base(details.Shell)

	// if we have already done so once, don't do it again
	if slices.Contains(vmconfig.GetState().SetupState.EditedShellProfiles, shellBase) {
		logrus.Infof("not attempting to modify shell profile for %s (%s), as we have already done so once", shellBase, details.Shell)
		return shellProfileEditResult{}, nil
	}

	switch shellBase {
	case "zsh":
		// what's the ZDOTDIR?
		// no need for -i (interactive), ZDOTDIR must be in .zshenv
		zdotdir := details.EnvZDOTDIR
		if zdotdir == "" {
			zdotdir = details.Home
		}

		profilePath := zdotdir + "/.zprofile"
		initSnippetPath := conf.ShellInitDir() + "/init.zsh"
		return editShellProfile(shellBase, profilePath, initSnippetPath)

	case "bash":
		// prefer bash_profile, otherwise use profile
		profilePath := details.Home + "/.bash_profile"
		if err := unix.Access(profilePath, unix.F_OK); errors.Is(err, os.ErrNotExist) {
			profilePath = details.Home + "/.profile"
		}
		initSnippetPath := conf.ShellInitDir() + "/init.bash"
		return editShellProfile(shellBase, profilePath, initSnippetPath)

	case "fish":
		profilePath := details.Home + "/.config/fish/config.fish"
		initSnippetPath := conf.ShellInitDir() + "/init2.fish"
		return editShellProfile(shellBase, profilePath, initSnippetPath)

	default:
		// we don't know how to deal with this.
		// just ask the user to add it to their path
		logrus.Info("unknown shell, asking user to add bins to PATH")
		return shellProfileEditResult{
			AlertAddPath: true,
		}, nil
	}
}

type SetupState struct {
	AdminLinkCommands []vmtypes.PHSymlinkRequest
	AdminLinkDocker   bool
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

	// get env, fix PATH
	details, err := s.setupUserDetailsOnce()
	if err != nil {
		return nil, err
	}

	// write SSH key and config
	// depends on PATH for edge cases like ssh_config Match exec "type orb"
	err = s.setupPublicSSH()
	if err != nil {
		// TODO: fix ssh config parsing for env vars in Host blocks
		logrus.WithError(err).Warn("failed to set up SSH config")
	}

	// split path
	pathItems := strings.Split(details.EnvPATH, ":")

	// link docker sock?
	setupState := SetupState{}
	useAdmin := vmconfig.Get().Setup_UseAdmin
	if useAdmin {
		sockDest, err := os.Readlink("/var/run/docker.sock")
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// doesn't exist - ignore, relink
			} else if errors.Is(err, unix.EINVAL) {
				// not a link - ignore, relink
			} else {
				return nil, err
			}
		}
		wantDest := conf.DockerSocket()
		if sockDest != wantDest {
			logrus.Info("[request admin] link docker socket")
			setupState.AdminLinkCommands = append(setupState.AdminLinkCommands, vmtypes.PHSymlinkRequest{Src: wantDest, Dest: "/var/run/docker.sock"})
			setupState.AdminLinkDocker = true
		}
	}

	// we have a relatively simple policy for command symlinking now:
	// - always link to ~/.orbstack/bin, and add it to shell profile.
	// - always link to /usr/local/bin if root, and if old links aren't homebrew (i.e. path includes .app)
	//   * /etc/paths includes /usr/local/bin by default (even though the dir doesn't actually exist), so apps that don't use the shell PATH (e.g. VS Code dev containers?) or that hard-code /usr/local/bin (e.g. Jetbrains Docker plugin) will still work if we link it there
	//
	// it's not worth messing with ~/bin or ~/.local/bin:
	// - users get mad
	// - adding an app-specific bin to shell profile is usually accepted: rustup, jetbrains, homebrew, etc. do it
	// - existing shell sessions that use shell PATH are ok because they probably have a working docker CLI already. user will probably restart the shells before the stale symlinks become a problem.
	// - always showing "Command-Line Tools Installed" tells the user to restart the terminal to use the new tools.
	linkCmd := func(cmdSrc string, cmdName string) error {
		var errs []error

		// 1. always link to ~/.orbstack/bin
		userAppBinDirCmd := conf.UserAppBinDir() + "/" + cmdName
		errs = append(errs, symlinkIfChanged(cmdSrc, userAppBinDirCmd))

		// 2. if there's an old symlink at ~/.local/bin (or ~/bin), change it to point to ~/.orbstack/bin
		// this fixes old ~/.local/bin symlinks but also lets the user keep symlinks in ~/.local/bin if they want
		// in the past, we used to create links at ~/.local/bin and ~/bin if they existed, but we no longer do that
		// instead, we just always create ~/.orbstack/bin links
		// so we need to change the old links or they'll stop working if the binary is ever moved, etc.
		errs = append(errs, symlinkExistingAppIfChanged(userAppBinDirCmd, details.Home+"/.local/bin/"+cmdName))
		errs = append(errs, symlinkExistingAppIfChanged(userAppBinDirCmd, details.Home+"/bin/"+cmdName))

		// 3. if we have admin, link to /usr/local/bin, unless there's an existing, non-broken link that doesn't point to *.app
		if useAdmin {
			shouldLink, err := shouldSymlinkApp(cmdSrc, "/usr/local/bin/"+cmdName)
			if err != nil {
				errs = append(errs, err)
			} else if shouldLink {
				setupState.AdminLinkCommands = append(setupState.AdminLinkCommands, vmtypes.PHSymlinkRequest{Src: cmdSrc, Dest: "/usr/local/bin/" + cmdName})
			}
		}

		return errors.Join(errs...)
	}
	for _, cmd := range binCommands {
		err = linkCmd(conf.CliBinDir()+"/"+cmd, cmd)
		if err != nil {
			return nil, err
		}
	}
	for _, cmd := range xbinCommands {
		err = linkCmd(conf.CliXbinDir()+"/"+cmd, cmd)
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

	shellProfileResult, err := s.tryModifyShellProfile(details, pathItems)
	if err != nil {
		return nil, err
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
		err = symlinkIfChanged(pluginSrc, pluginDest)
		if err != nil {
			return nil, err
		}
	}

	// need to fix docker creds store?
	// in case user uninstalled Docker Desktop, we need to change it
	// we don't consider this critical, so ignore errors
	err = dockerconf.FixDockerCredsStore()
	if err != nil {
		logrus.WithError(err).Warn("failed to fix docker creds store")
	}

	info := &vmtypes.SetupInfo{}
	if len(setupState.AdminLinkCommands) > 0 {
		// join and escape commands
		info.AdminSymlinkCommands = setupState.AdminLinkCommands

		// whether macOS appends suffix ~40 chars but not exactly. width based?
		// so do it for each message: "OrbStack is trying to install a new helper tool."
		var msg string
		if len(setupState.AdminLinkCommands) > 0 && setupState.AdminLinkDocker {
			msg = "Improve Docker socket compatibility and install command-line tools? Optional; learn more at orbsta.cc/admin.\n\n"
		} else if len(setupState.AdminLinkCommands) > 0 {
			msg = "Install command-line tools? Optional; learn more at orbsta.cc/admin.\n\n"
		} else if setupState.AdminLinkDocker {
			msg = "Improve Docker socket compatibility? Optional; learn more at orbsta.cc/admin.\n\n"
		}
		info.AdminMessage = &msg
	}
	if shellProfileResult.AlertAddPath && !vmconfig.GetState().SetupState.PathUpdateRequested {
		info.AlertRequestAddPath = true

		err = vmconfig.UpdateState(func(state *vmconfig.VmgrState) error {
			state.SetupState.PathUpdateRequested = true
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	// the "profile changed / CLI tools installed" alert tells users to restart terminals for PATH update if we don't have admin
	// if user has admin, they'll probably allow it, so we'll symlink into /usr/local/bin. that removes the need to restart terminals
	if shellProfileResult.AlertProfileChanged && len(setupState.AdminLinkCommands) == 0 {
		info.AlertProfileChanged = true
	}
	logrus.WithFields(logrus.Fields{
		"adminSymlinkCommands": info.AdminSymlinkCommands,
		"adminMessage":         strp(info.AdminMessage),
		"alertAddPath":         info.AlertRequestAddPath,
		"alertProfileChanged":  info.AlertProfileChanged,
	}).Debug("prepare setup info done")

	s.setupDone = true
	logrus.Info("setup done")
	return info, nil
}

// for CLI-only, this completes the setup without GUI
func completeSetupCli(info *vmtypes.SetupInfo) error {
	// notify profile changed
	if info.AlertProfileChanged {
		logrus.Info("notifying profile changed")
		err := guihelper.Notify(guitypes.Notification{
			Title:   "Command-Line Tools Installed",
			Message: "Shell profile (PATH) has been updated. Restart terminal to use new tools.",
		})
		if err != nil {
			return err
		}
	}

	// notify add paths
	if info.AlertRequestAddPath {
		logrus.Info("notifying add paths")
		err := guihelper.Notify(guitypes.Notification{
			Title:   "Install Command-Line Tools",
			Message: "To use command-line tools, add ~/.orbstack/bin to your PATH.",
		})
		if err != nil {
			return err
		}
	}

	// request run as admin
	if len(info.AdminSymlinkCommands) > 0 {
		logrus.WithField("cmd", info.AdminSymlinkCommands).Debug("requesting admin symlinks")
		if info.AdminMessage != nil {
			swext.PrivhelperSetInstallReason(*info.AdminMessage)
		}

		for _, cmd := range info.AdminSymlinkCommands {
			err := swext.PrivhelperSymlink(cmd.Src, cmd.Dest)
			if err != nil {
				if err.Error() == "canceled" {
					logrus.Info("user canceled privhelper install")
					break
				} else if err.Error() == "canceledAndReachedMaxDismissCount" {
					logrus.Info("user canceled privhelper install too many times, disabling")

					// disable admin
					err := vmconfig.Update(func(c *vmtypes.VmConfig) {
						c.Setup_UseAdmin = false
					})
					if err != nil {
						logrus.WithError(err).Warn("failed to disable admin")
					}
					break
				} else {
					return fmt.Errorf("symlink %s -> %s: %w", cmd.Src, cmd.Dest, err)
				}
			}
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
