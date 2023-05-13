package osver

import (
	"github.com/orbstack/macvirt/macvmgr/syncx"
	"golang.org/x/mod/semver"
	"golang.org/x/sys/unix"
)

var (
	onceVersion syncx.Once[string]
)

func readVersion() (string, error) {
	vstr, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return "", err
	}

	return "v" + vstr, nil
}

func Get() string {
	return onceVersion.Do(func() string {
		v, err := readVersion()
		if err != nil {
			panic(err)
		}

		return v
	})
}

func IsAtLeast(v string) bool {
	return semver.Compare(Get(), v) >= 0
}
