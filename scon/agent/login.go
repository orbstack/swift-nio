package agent

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/scon/util"
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
		// do in background
		go func() {
			err := m.onUserStart(user)
			if err != nil {
				logrus.WithError(err).Error("Failed to start user session")
			}
		}()
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
	err := retryRunLogind("loginctl", "enable-linger", user)
	if err != nil {
		return err
	}

	return nil
}

func (m *LoginManager) onUserStop(user string) error {
	m.actionMu.Lock()
	defer m.actionMu.Unlock()

	logrus.WithField("user", user).Debug("stopping user session")

	// we don't stop sessions for now
	return nil
}

func retryRunLogind(cmd ...string) error {
	// if no loginctl, don't bother
	if _, err := exec.LookPath("loginctl"); err != nil {
		return nil
	}

	start := time.Now()
	for {
		err := util.Run(cmd...)
		if err == nil {
			return nil
		}

		time.Sleep(nixosPollInterval)
		if time.Since(start) > nixosBootTimeout {
			return fmt.Errorf("timeout waiting for logind: %w", err)
		}
	}
}
