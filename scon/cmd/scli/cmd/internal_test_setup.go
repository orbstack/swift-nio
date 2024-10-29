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

	c := 0
	for c < 5 {
		if vmclient.IsRunning() {
			break
		}

		time.Sleep(time.Second / 2)
		c++
	}

	if !vmclient.IsRunning() {
		return errors.New("vmgr still not running after 5 /2s")
	}

	runningWithTestsAfterRespawn, err := vmclient.Client().InternalIsRunningForTests()
	if err != nil {
		return fmt.Errorf("check vmgr status: %w", err)
	}

	if runningWithTestsAfterRespawn != withTests {
		return fmt.Errorf("expected with tests = %v, got %v", withTests, runningWithTestsAfterRespawn)
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
				runningWithTests, err := vmclient.Client().InternalIsRunningForTests()
				if err != nil {
					return fmt.Errorf("check vmgr status: %w", err)
				}

				if runningWithTests {
					fmt.Fprintln(os.Stderr, "vmgr already running for tests, doing nothing")
					os.Remove(execInfoPath)
					return nil
				}

				vmgrExePath, err := vmclient.FindVmgrExe()
				if err != nil {
					return fmt.Errorf("find vmgr exe: %w", err)
				}

				err = vmclient.Client().Stop()
				if err != nil {
					return fmt.Errorf("stop vmgr: %w", err)
				}

				err = spawnVmgr(vmgrExePath, true)
				if err != nil {
					return err
				}

				err = writeExecInfo(vmgrExePath, true)
				if err != nil {
					return err
				}
			} else {
				vmgrExePath, err := vmclient.FindVmgrExe()
				if err != nil {
					return fmt.Errorf("find vmgr exe: %w", err)
				}

				err = spawnVmgr(vmgrExePath, true)
				if err != nil {
					return err
				}

				err = writeExecInfo(vmgrExePath, false)
				if err != nil {
					return err
				}
			}
		case "post":
			if _, err := os.Stat(execInfoPath); err == nil {
				b, err := os.ReadFile(execInfoPath)
				if err != nil {
					return fmt.Errorf("read exec info: %w", err)
				}

				var execInfo testExecInfo
				err = json.Unmarshal(b, &execInfo)
				if err != nil {
					return fmt.Errorf("unmarshal exec info: %w", err)
				}

				err = vmclient.Client().Stop()
				if err != nil {
					return fmt.Errorf("stop vmgr: %w", err)
				}

				if execInfo.KeepRunning {
					err = spawnVmgr(execInfo.ExePath, false)
					if err != nil {
						return fmt.Errorf("spawn vmgr: %w", err)
					}
				}

				os.Remove(execInfoPath)
			}
		default:
			return fmt.Errorf("unexpected argument %v, expected \"pre\" or \"post\"", args[0])
		}

		return nil
	},
}
