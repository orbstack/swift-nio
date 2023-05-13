package agent

import (
	"os/exec"
	"sync"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type LoginManager struct {
	refsMu   sync.Mutex
	userRefs map[string]int

	actionMu sync.Mutex
}

func NewLoginManager() *LoginManager {
	return &LoginManager{
		userRefs: make(map[string]int),
	}
}

func (m *LoginManager) BeginUserSession(user string) {
	m.refsMu.Lock()
	defer m.refsMu.Unlock()
	m.userRefs[user]++
	if m.userRefs[user] == 1 {
		// do in foreground, or running programs could fail (/run/user/UID doesn't exist)
		// example: vscode ln -sf auth sock
		err := m.onUserStart(user)
		if err != nil {
			logrus.WithError(err).Error("Failed to start user session")
		}
	}
}

func (m *LoginManager) EndUserSession(user string) {
	m.refsMu.Lock()
	defer m.refsMu.Unlock()
	m.userRefs[user]--
	if m.userRefs[user] == 0 {
		delete(m.userRefs, user)
		// do in background
		go func() {
			err := m.onUserStop(user)
			if err != nil {
				logrus.WithError(err).Error("failed to stop user session")
			}
		}()
	}
}

func (m *LoginManager) onUserStart(user string) error {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()

	logrus.WithField("user", user).Debug("starting user session")

	// loginctl enable-linger $user
	// this starts per-user systemd instance
	err := waitRunLogind("loginctl", "enable-linger", user)
	if err != nil {
		return err
	}

	return nil
}

func (m *LoginManager) onUserStop(user string) error {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()

	logrus.WithField("user", user).Debug("stopping user session")

	// we don't stop sessions for now,
	// to avoid disrupting users running "loginctl enable-linger" themselves
	return nil
}

func waitRunLogind(cmd ...string) error {
	// if no loginctl, don't bother
	if _, err := exec.LookPath("loginctl"); err != nil {
		return nil
	}

	// wait for logind to start
	err := util.WaitForRunPathExist("/run/systemd/units/invocation:systemd-logind.service")
	if err != nil {
		return err
	}

	// run command
	return util.Run(cmd...)
}
