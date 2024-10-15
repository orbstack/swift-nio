package tests

import (
	"context"
	_ "embed"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"golang.org/x/sync/semaphore"
)

//go:embed cloud-init.yml
var cloudInitUserData string

func getTestMachineCount() int64 {
	env, ok := os.LookupEnv("ORB_TEST_MACHINE_COUNT")
	if !ok {
		cpuc, err := exec.Command("sysctl", "-n", "hw.ncpu").Output()
		if err != nil {
			panic(fmt.Errorf("couldn't get cpu count: %w", err))
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(cpuc)))
		if err != nil {
			panic(fmt.Errorf("couldn't parse cpu count %v: %w", strings.TrimSpace(string(cpuc)), err))
		}
		return int64(n / 2)
	}

	n, err := strconv.Atoi(env)
	if err != nil {
		panic(fmt.Errorf("couldn't parse $ORB_TEST_MACHINE_COUNT value %v: %w", env, err))
	}

	return int64(n)
}

// to avoid hammering host, only run $ORB_TEST_MACHINE_COUNT (or ncpu/2) test machines at a time
var testMachineSem = semaphore.NewWeighted(getTestMachineCount())

func init() {
	// don't try to spawn daemon
	scli.Conf().ControlVM = false
}

var testPrefix = sync.OnceValue(func() string {
	return fmt.Sprintf("itest-%d", rand.Uint64())
})

func checkT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func forEachDistroArchVer(t *testing.T, f func(t *testing.T, distro, ver, arch, machineName string)) {
	t.Helper()
	for _, distro := range images.Distros() {
		// short test: only test alpine
		if testing.Short() && distro != "alpine" {
			continue
		}

		distro := distro
		t.Run(distro, func(t *testing.T) {
			t.Parallel()

			img := images.DistroToImage[distro]

			// version combos in non-short
			testVersions := []string{images.ImageToLatestVersion[img]}
			if oldestVer, ok := images.ImageToOldestVersion[distro]; ok && !testing.Short() {
				testVersions = append(testVersions, oldestVer)
			}

			// handle cases where oldest == latest, e.g. CentOS 9 Stream
			if len(testVersions) == 2 && testVersions[0] == testVersions[1] {
				testVersions = []string{testVersions[0]}
			}

			for _, ver := range testVersions {
				ver := ver
				t.Run(ver, func(t *testing.T) {
					t.Parallel()

					for _, arch := range images.Archs() {
						// short test: only test host arch
						if testing.Short() && arch != runtime.GOARCH {
							continue
						}

						// skip unsupported combo: nixos amd64 on arm64 host
						if distro == "nixos" && arch == "amd64" && runtime.GOARCH == "arm64" {
							continue
						}

						arch := arch
						t.Run(arch, func(t *testing.T) {
							t.Parallel()

							machineName := fmt.Sprintf("%s-%s-%s-%s", testPrefix(), distro, strings.Replace(ver, ".", "d", -1), arch)
							f(t, distro, ver, arch, machineName)
						})
					}
				})
			}
		})
	}
}

func TestSconPing(t *testing.T) {
	err := scli.Client().Ping()
	checkT(t, err)
}

func TestSconMachines(t *testing.T) {
	forEachDistroArchVer(t, func(t *testing.T, distro, ver, arch, machineName string) {
		if os.Getenv("ORB_SKIP_TESTS") == "1" {
			t.Skip("skipping per request")
		}

		err := testMachineSem.Acquire(context.TODO(), 1)
		checkT(t, err)
		cleanup(t, func() error {
			testMachineSem.Release(1)
			return nil
		})

		container, err := scli.Client().Create(types.CreateRequest{
			Name: machineName,
			Image: types.ImageSpec{
				Distro:  images.DistroToImage[distro],
				Arch:    arch,
				Version: ver,
			},
			InternalForTesting: true,
		})
		if err != nil && strings.Contains(err.Error(), "image not found") {
			t.Skip("image not found")
		} else {
			checkT(t, err)
		}

		t.Run("GetByName", func(t *testing.T) {
			containers, err := scli.Client().ListContainers()
			checkT(t, err)

			if !slices.ContainsFunc(containers, func(c types.ContainerRecord) bool {
				return c.Name == machineName
			}) {
				t.Fatalf("container %s not found", machineName)
			}
		})

		t.Run("GetByID", func(t *testing.T) {
			c, err := scli.Client().GetByID(container.ID)
			checkT(t, err)

			if c.ID != container.ID && c.Name != machineName {
				t.Fatalf("expected machine named %s with ID %s, got machine named %s with ID %s", machineName, container.ID, c.Name, c.ID)
			}
		})

		t.Run("Stop", func(t *testing.T) {
			checkT(t, scli.Client().ContainerStop(container))
		})

		t.Run("Start", func(t *testing.T) {
			checkT(t, scli.Client().ContainerStart(container))
		})

		t.Run("Restart", func(t *testing.T) {
			checkT(t, scli.Client().ContainerRestart(container))
		})

		t.Run("Rename", func(t *testing.T) {
			newName := machineName + "-renamed"
			err := scli.Client().ContainerRename(container, newName)
			checkT(t, err)

			args := []string{"orbctl", "run", "-m", newName}

			hostnameCmd := []string{"hostname"}
			if distro == "oracle" {
				// Oracle Linux doesn't ship a `hostname` binary, lol
				// Update from two months later: `hostnamectl hostname` no longer works?!
				// WTF is Oracle doing?
				hostnameCmd = []string{"bash", "-c", `hostnamectl | grep "Static hostname:"`}
			}

			args = append(args, hostnameCmd...)

			output, err := runScli(args...)
			checkT(t, err)

			if distro == "oracle" {
				output = strings.TrimPrefix(strings.TrimSpace(string(output)), "Static hostname: ")
			}

			if strings.TrimSpace(string(output)) != newName {
				t.Fatalf("expected machine's hostname to be %s, got %s", newName, strings.TrimSpace(string(output)))
			}

			err = scli.Client().ContainerRename(container, machineName)
			checkT(t, err)
		})

		t.Run("GetRuntimeLogs", func(t *testing.T) {
			lxcLogs, err := scli.Client().ContainerGetLogs(container, types.LogRuntime)
			checkT(t, err)
			if len(lxcLogs) == 0 {
				t.Fatal("no logs")
			}
		})

		t.Run("GetConsoleLogs", func(t *testing.T) {
			lxcLogs, err := scli.Client().ContainerGetLogs(container, types.LogConsole)
			checkT(t, err)
			if len(lxcLogs) == 0 {
				t.Fatal("no logs")
			}
		})

		t.Run("CloudInit", func(t *testing.T) {
			machineName := machineName + "-cinit"
			container, err := scli.Client().Create(types.CreateRequest{
				Name: machineName,
				Image: types.ImageSpec{
					Distro:  images.DistroToImage[distro],
					Arch:    arch,
					Version: ver,
				},
				CloudInitUserData:  cloudInitUserData,
				InternalForTesting: true,
			})
			if err != nil {
				if strings.Contains(err.Error(), "cloud-init not supported") || strings.Contains(err.Error(), "image not found") {
					t.Skip("cloud-init not supported")
				} else {
					checkT(t, err)
				}
			}
			cleanup(t, func() error {
				return scli.Client().ContainerDelete(container)
			})

			// check file
			out, err := runScli("orbctl", "run", "-m", machineName, "cat", "/etc/cltest")
			checkT(t, err)
			if out != "it works!\n" {
				t.Fatalf("expected test, got: %s", out)
			}
		})

		err = scli.Client().ContainerDelete(container)
		checkT(t, err)

		_, err = scli.Client().GetByID(container.ID)
		if err == nil {
			t.Fatal("was able to retrieve container by ID after deletion")
		}
	})
}
