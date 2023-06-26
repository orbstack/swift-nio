package appver

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

//go:generate ./gen_version.sh

var (
	//go:embed version.txt
	versionData string
)

type Version struct {
	Short       string
	Code        int
	GitDescribe string
	GitCommit   string
}

var (
	onceVersion syncx.Once[*Version]
)

func Get() *Version {
	return onceVersion.Do(func() *Version {
		lines := strings.Split(versionData, "\n")
		describe := lines[0]
		var short string
		dashParts := strings.Split(describe, "-")
		dashParts[0] = strings.TrimPrefix(dashParts[0], "v")
		var rcNum int
		if strings.Contains(describe, "-rc") {
			short = dashParts[0] + "-" + dashParts[1]
			var err error
			rcNum, err = strconv.Atoi(strings.TrimPrefix(dashParts[1], "rc"))
			if err != nil {
				panic(err)
			}
		} else {
			// simple case
			short = dashParts[0]
		}

		// Leave 100 least significant: 50 for hotfix, 50 for next test
		segs := strings.Split(strings.Split(short, "-")[0], ".")
		major, err := strconv.Atoi(segs[0])
		if err != nil {
			panic(err)
		}
		minor, err := strconv.Atoi(segs[1])
		if err != nil {
			panic(err)
		}
		patch, err := strconv.Atoi(segs[2])
		if err != nil {
			panic(err)
		}
		code := major*100*100*100 + minor*100*100 + patch*100
		if rcNum > 0 {
			code = code - 50 + rcNum
		}

		return &Version{
			Short:       short,
			Code:        code,
			GitDescribe: describe,
			GitCommit:   lines[1],
		}
	})
}
