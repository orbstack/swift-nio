package tests

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/syncx"
)

var (
	onceTestPrefix syncx.Once[string]
)

func init() {
	// don't try to spawn daemon
	scli.Conf().ControlVM = false
}

func testPrefix() string {
	return onceTestPrefix.Do(func() string {
		return fmt.Sprintf("itest-%d", rand.Uint64())
	})
}

func checkT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func forEachDistroArchVer(t *testing.T, f func(distro, ver, arch, machineName string)) {
	t.Helper()
	for _, distro := range images.Distros() {
		// short test: only test alpine
		if testing.Short() && distro != "alpine" {
			continue
		}

		distro := distro
		t.Run(distro, func(t *testing.T) {
			t.Parallel()

			// version combos in non-short
			testVersions := []string{"" /*default latest*/}
			if oldestVer, ok := images.ImageToOldestVersion[distro]; ok && !testing.Short() {
				testVersions = append(testVersions, oldestVer)
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
							f(distro, ver, arch, machineName)
						})
					}
				})
			}
		})
	}
}

func forEachDistroArchVerGet(t *testing.T, f func(distro, ver, arch, machineName string, c *types.ContainerRecord)) {
	forEachDistroArchVer(t, func(distro, ver, arch, machineName string) {
		c, err := scli.Client().GetByName(machineName)
		checkT(t, err)

		f(distro, ver, arch, machineName, c)
	})
}

func TestSconPing(t *testing.T) {
	err := scli.Client().Ping()
	checkT(t, err)
}

func TestSconCreate(t *testing.T) {
	forEachDistroArchVer(t, func(distro, ver, arch, machineName string) {
		_, err := scli.Client().Create(types.CreateRequest{
			Name: machineName,
			Image: types.ImageSpec{
				Distro:  images.DistroToImage[distro],
				Arch:    arch,
				Version: ver,
			},
		})
		checkT(t, err)
	})
}

func TestSconList(t *testing.T) {
	containers, err := scli.Client().ListContainers()
	checkT(t, err)

	forEachDistroArchVer(t, func(distro, ver, arch, machineName string) {
		for _, c := range containers {
			if c.Name == machineName {
				//TODO validate image info
				return
			}
		}

		t.Fatalf("container %s not found", machineName)
	})
}

func TestSconGetByName(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		if c.Name != machineName {
			t.Fatalf("expected %s, got %s", machineName, c.Name)
		}
	})
}

func TestSconGetByID(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		c2, err := scli.Client().GetByID(c.ID)
		checkT(t, err)

		if *c != *c2 {
			t.Fatalf("expected %v, got %v", c, c2)
		}
	})
}

func TestSconStop(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerStop(c)
		checkT(t, err)
	})
}

func TestSconStart(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerStart(c)
		checkT(t, err)
	})
}

func TestSconRestart(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerRestart(c)
		checkT(t, err)
	})
}

func TestSconRename(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerRename(c, machineName+"_renamed")
		checkT(t, err)

		//TODO verify name in shell, hosts etc.

		// restore name
		err = scli.Client().ContainerRename(c, machineName)
		checkT(t, err)
	})
}

func TestSconGetRuntimeLogs(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		lxcLogs, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
		checkT(t, err)
		if len(lxcLogs) == 0 {
			t.Fatal("no logs")
		}

		//TODO verify logs
	})
}

func TestSconGetConsoleLogs(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		lxcLogs, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
		checkT(t, err)
		if len(lxcLogs) == 0 {
			t.Fatal("no logs")
		}

		//TODO verify logs
	})
}

func TestSconDelete(t *testing.T) {
	forEachDistroArchVerGet(t, func(distro, ver, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerDelete(c)
		checkT(t, err)
	})
}

func TestSconListAfterDelete(t *testing.T) {
	containers, err := scli.Client().ListContainers()
	checkT(t, err)

	forEachDistroArchVer(t, func(distro, ver, arch, machineName string) {
		for _, c := range containers {
			if c.Name == machineName {
				t.Fatalf("container %s still exists", machineName)
			}
		}
	})
}
