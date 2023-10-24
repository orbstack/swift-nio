package osver

import (
	"strings"
	"sync"

	"golang.org/x/mod/semver"
	"golang.org/x/sys/unix"
)

func readVersion() (string, error) {
	vstr, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return "", err
	}

	return "v" + vstr, nil
}

var Get = sync.OnceValue(func() string {
	v, err := readVersion()
	if err != nil {
		panic(err)
	}

	return v
})

func IsAtLeast(v string) bool {
	return semver.Compare(Get(), v) >= 0
}

func Major() string {
	return strings.TrimPrefix(semver.Major(Get()), "v")
}

func Build() string {
	ver, err := unix.Sysctl("kern.osversion")
	if err != nil {
		panic(err)
	}

	return ver
}
