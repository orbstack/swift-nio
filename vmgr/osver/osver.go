package osver

import (
	"strconv"
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

func Major() uint16 {
	ver, err := strconv.Atoi(strings.TrimPrefix(semver.Major(Get()), "v"))
	if err != nil {
		panic(err)
	}

	return uint16(ver)
}

func Build() string {
	ver, err := unix.Sysctl("kern.osversion")
	if err != nil {
		panic(err)
	}

	return ver
}
