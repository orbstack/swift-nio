package updates

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/sirupsen/logrus"
)

type Updater struct {
	client *http.Client
}

func NewUpdater() *Updater {
	return &Updater{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:    2,
				IdleConnTimeout: 5 * time.Minute,
			},
		},
	}
}

func getFeedURL() string {
	return fmt.Sprintf("https://api-updates.orbstack.dev/%s/appcast.xml", runtime.GOARCH)
}

func FindBundle() (string, error) {
	sparkleExe, err := conf.FindSparkleExe()
	if err != nil {
		return "", err
	}

	bundlePath := strings.TrimSuffix(path.Dir(sparkleExe), "/Contents/MacOS")
	return bundlePath, nil
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
		bundlePath = "/Users/dragon/Library/Caches/JetBrains/AppCode2022.3/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app"
		sparkleExe = bundlePath + "/Contents/MacOS/sparkle-cli"
		bundlePath = "/Applications/OrbStack.app"
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
	}

	return nil
}
