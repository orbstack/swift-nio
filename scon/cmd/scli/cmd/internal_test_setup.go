//go:build !release

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/spf13/cobra"
)

const vmgrStartTimeout = 10 * time.Second

func init() {
	internalCmd.AddCommand(internalTestSetup)
}

func spawnVmgr(path string, withTests bool) error {
	cmd := pspawn.Command(path, "spawn-daemon")
	if withTests {
		cmd.Env = append(cmd.Environ(), "ORB_TEST=1")
	}
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("spawn vmgr: %w", err)
	}

	start := time.Now()
	for time.Since(start) < vmgrStartTimeout {
		if vmclient.IsRunning() {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if !vmclient.IsRunning() {
		return errors.New("vmgr not running after " + vmgrStartTimeout.String())
	}

	isTestMode, err := vmclient.Client().InternalIsTestMode()
	if err != nil {
		return fmt.Errorf("check vmgr status: %w", err)
	}

	if isTestMode != withTests {
		return fmt.Errorf("expected with tests = %v, got %v", withTests, isTestMode)
	}

	return nil
}

type testExecInfo struct {
	ExePath     string
	KeepRunning bool
}

var execInfoPath = coredir.AppDir() + "/test-exec-info.json"

func writeExecInfo(exePath string, keepRunning bool) error {
	b, err := json.Marshal(testExecInfo{ExePath: exePath, KeepRunning: keepRunning})
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	err = os.WriteFile(execInfoPath, b, 0644)
	if err != nil {
		return fmt.Errorf("write exec info: %w", err)
	}

	return nil
}

var internalTestSetup = &cobra.Command{
	Use:  "test-setup {pre | post}",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "pre":
			if vmclient.IsRunning() {
				// NOTE: We use vmgr's vmcontrol client funcs here, so that we don't bump into updateVmgr from scli -- we don't
				// want to change the running vmgr binary if there is one.
				isTestMode, err := vmclient.Client().InternalIsTestMode()
				if err != nil {
					checkCLI(fmt.Errorf("check vmgr status: %w", err))
				}

				if isTestMode {
					fmt.Fprintln(os.Stderr, "vmgr already running for tests, doing nothing")
					_ = os.Remove(execInfoPath)
					return nil
				}

				vmgrExePath, err := vmclient.FindVmgrExe()
				if err != nil {
					checkCLI(fmt.Errorf("find vmgr exe: %w", err))
				}

				err = vmclient.Client().Stop()
				if err != nil {
					checkCLI(fmt.Errorf("stop vmgr: %w", err))
				}

				err = spawnVmgr(vmgrExePath, true)
				checkCLI(err)

				err = writeExecInfo(vmgrExePath, true)
				checkCLI(err)
			} else {
				vmgrExePath, err := vmclient.FindVmgrExe()
				if err != nil {
					checkCLI(fmt.Errorf("find vmgr exe: %w", err))
				}

				err = spawnVmgr(vmgrExePath, true)
				checkCLI(err)

				err = writeExecInfo(vmgrExePath, false)
				checkCLI(err)
			}

		case "post":
			if _, err := os.Stat(execInfoPath); err == nil {
				b, err := os.ReadFile(execInfoPath)
				if err != nil {
					checkCLI(fmt.Errorf("read exec info: %w", err))
				}

				var execInfo testExecInfo
				err = json.Unmarshal(b, &execInfo)
				if err != nil {
					checkCLI(fmt.Errorf("unmarshal exec info: %w", err))
				}

				err = vmclient.Client().Stop()
				if err != nil {
					checkCLI(fmt.Errorf("stop vmgr: %w", err))
				}

				if execInfo.KeepRunning {
					err = spawnVmgr(execInfo.ExePath, false)
					if err != nil {
						checkCLI(fmt.Errorf("spawn vmgr: %w", err))
					}
				}

				_ = os.Remove(execInfoPath)
			}

		default:
			return fmt.Errorf("unexpected argument %v, expected 'pre' or 'post'", args[0])
		}

		return nil
	},
}
