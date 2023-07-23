package tests

import (
	"fmt"
	"math/rand"
	"runtime"
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
		return fmt.Sprintf("itest_%d", rand.Uint64())
	})
}

func checkTest(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func forEachDistroArch(t *testing.T, f func(distro, arch, machineName string)) {
	for _, distro := range images.Distros() {
		// short test: only test alpine
		if testing.Short() && distro != "alpine" {
			continue
		}

		distro := distro
		t.Run(distro, func(t *testing.T) {
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

					f(distro, arch, fmt.Sprintf("%s_%s_%s", testPrefix(), distro, arch))
				})
			}
		})
	}
}

func forEachDistroArchGet(t *testing.T, f func(distro, arch, machineName string, c *types.ContainerRecord)) {
	forEachDistroArch(t, func(distro, arch, machineName string) {
		c, err := scli.Client().GetByName(machineName)
		checkTest(t, err)

		f(distro, arch, machineName, c)
	})
}

func TestSconPing(t *testing.T) {
	err := scli.Client().Ping()
	checkTest(t, err)
}

func TestSconCreate(t *testing.T) {
	forEachDistroArch(t, func(distro, arch, machineName string) {
		_, err := scli.Client().Create(types.CreateRequest{
			Name: machineName,
			Image: types.ImageSpec{
				Distro: distro,
				Arch:   arch,
			},
		})
		checkTest(t, err)
	})
}

func TestSconList(t *testing.T) {
	containers, err := scli.Client().ListContainers()
	checkTest(t, err)

	forEachDistroArch(t, func(distro, arch, machineName string) {
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
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		if c.Name != machineName {
			t.Fatalf("expected %s, got %s", machineName, c.Name)
		}
	})
}

func TestSconGetByID(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		c2, err := scli.Client().GetByID(c.ID)
		checkTest(t, err)

		if *c != *c2 {
			t.Fatalf("expected %v, got %v", c, c2)
		}
	})
}

func TestSconStop(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerStop(c)
		checkTest(t, err)
	})
}

func TestSconStart(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerStart(c)
		checkTest(t, err)
	})
}

func TestSconRestart(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerRestart(c)
		checkTest(t, err)
	})
}

func TestSconRename(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerRename(c, machineName+"_renamed")
		checkTest(t, err)

		//TODO verify name in shell, hosts etc.

		// restore name
		err = scli.Client().ContainerRename(c, machineName)
		checkTest(t, err)
	})
}

func TestSconGetRuntimeLogs(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		lxcLogs, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
		checkTest(t, err)
		if len(lxcLogs) == 0 {
			t.Fatal("no logs")
		}

		//TODO verify logs
	})
}

func TestSconGetConsoleLogs(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		lxcLogs, err := scli.Client().ContainerGetLogs(c, types.LogRuntime)
		checkTest(t, err)
		if len(lxcLogs) == 0 {
			t.Fatal("no logs")
		}

		//TODO verify logs
	})
}

func TestSconDelete(t *testing.T) {
	forEachDistroArchGet(t, func(distro, arch, machineName string, c *types.ContainerRecord) {
		err := scli.Client().ContainerDelete(c)
		checkTest(t, err)
	})
}

func TestSconListAfterDelete(t *testing.T) {
	containers, err := scli.Client().ListContainers()
	checkTest(t, err)

	forEachDistroArch(t, func(distro, arch, machineName string) {
		for _, c := range containers {
			if c.Name == machineName {
				t.Fatalf("container %s still exists", machineName)
			}
		}
	})
}
