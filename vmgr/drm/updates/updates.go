package updates

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/drm/drmid"
	"github.com/orbstack/macvirt/vmgr/drm/timex"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/sirupsen/logrus"
)

const (
	// match SUScheduledCheckInterval
	updateCheckInterval = 4 * time.Hour
	notifyInterval      = 24 * time.Hour
)

type Updater struct {
	lastCheckTime  *timex.MonoSleepTime
	lastNotifyTime *timex.MonoSleepTime
	lastInfo       *UpdateInfo
}

func NewUpdater() *Updater {
	getFeedURL()
	return &Updater{}
}

func getFeedURL() string {
	// bucket logic matches Swift
	installID := drmid.ReadInstallID()

	// decode first 4 bytes as hex
	uuidBytes, err := hex.DecodeString(installID[:4*2])
	if err != nil {
		panic("invalid uuid: " + installID)
	}

	// bucket is the first 4 bytes of the install id
	id4 := binary.BigEndian.Uint32(uuidBytes)
	bucket := id4 % 100

	return fmt.Sprintf("https://api-updates.orbstack.dev/%s/appcast.xml?bucket=%d", runtime.GOARCH, bucket)
}

func NewSparkleCommand(args ...string) (*exec.Cmd, error) {
	sparkleExe, err := conf.FindSparkleExe()
	if err != nil {
		return nil, err
	}

	feedURL := getFeedURL()
	ver := appver.Get()
	userAgent := fmt.Sprintf("sparkle-cli vmgr/%s/%d/%s/%s", ver.Short, ver.Code, ver.GitDescribe, ver.GitCommit)
	bundlePath := strings.TrimSuffix(path.Dir(sparkleExe), "/Contents/MacOS")

	if conf.Debug() {
		bundlePath = "/Applications/OrbStack.app"
		sparkleExe = bundlePath + "/Contents/MacOS/sparkle-cli"
	}

	baseArgs := []string{"--user-agent-name", userAgent, "--feed-url", feedURL, "--send-profile", "--grant-automatic-checks", "--channels", "beta", "--allow-major-upgrades", bundlePath}
	allArgs := append(baseArgs, args...)
	cmd := exec.Command(sparkleExe, allArgs...)
	logrus.WithField("args", allArgs).Debug("sparkle-cli command")
	return cmd, nil
}

// sparkle schema
type UpdateInfo struct {
	Available bool
}

/*
	func (u *Updater) fetchAppcast() (*UpdateInfo, error) {
		// TODO
		resp, err := u.client.Get(getFeedURL())
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}

		m := make(map[any]any)
		err = xml.NewDecoder(resp.Body).Decode(&m)
		if err != nil {
			return nil, err
		}

		fmt.Println("fetched", m)

		return nil, nil
	}
*/
func (u *Updater) checkSparkleCli() (*UpdateInfo, error) {
	cmd, err := NewSparkleCommand("--probe", "--verbose")
	if err != nil {
		return nil, err
	}

	info := &UpdateInfo{}
	out, err := cmd.CombinedOutput()
	logrus.WithField("output", string(out)).Debug("sparkle-cli output")
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 4:
				// no updates
				info.Available = false
			default:
				return nil, fmt.Errorf("check sparkle update: %w; output: %s", err, string(out))
			}
		} else {
			return nil, fmt.Errorf("check sparkle update: %w; output: %s", err, string(out))
		}
	} else {
		info.Available = true
	}

	return info, nil
}

func (u *Updater) CheckNow() error {
	info, err := u.checkSparkleCli()
	if err != nil {
		return err
	}

	if info.Available {
		logrus.Info("update available")
		file := conf.UpdatePendingFlag()
		_, err := os.Stat(file)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}

			// create flag
			f, err := os.Create(file)
			if err != nil {
				return err
			}
			f.Close()
		}

		u.lastInfo = info

		err = u.MaybeNotify()
		if err != nil {
			return err
		}
	}

	return nil
}

func (u *Updater) MaybeNotify() error {
	if u.lastNotifyTime != nil && timex.SinceMonoSleep(*u.lastNotifyTime) < notifyInterval {
		return nil
	}

	if u.lastInfo == nil || !u.lastInfo.Available {
		return nil
	}

	err := guihelper.Notify(guitypes.Notification{
		Title:   "OrbStack Update Ready",
		Message: "Fixes, features, and improvements are available. Click to install.",
		URL:     appid.UrlUpdate,
		Silent:  true,
	})
	if err != nil {
		return err
	}

	now := timex.NowMonoSleep()
	u.lastNotifyTime = &now
	return nil
}

func (u *Updater) MaybeCheck() error {
	if u.lastCheckTime != nil && timex.SinceMonoSleep(*u.lastCheckTime) < updateCheckInterval {
		return nil
	}

	err := u.CheckNow()
	if err != nil {
		return err
	}

	now := timex.NowMonoSleep()
	u.lastCheckTime = &now
	return nil
}
