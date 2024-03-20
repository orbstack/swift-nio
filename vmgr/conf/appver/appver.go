package appver

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

const expSuffix = "rc"

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

var Get = sync.OnceValue(func() *Version {
	lines := strings.Split(versionData, "\n")
	describe := lines[0]
	var short string
	dashParts := strings.Split(describe, "-")
	dashParts[0] = strings.TrimPrefix(dashParts[0], "v")
	var rcNum int
	if strings.Contains(describe, "-"+expSuffix) {
		short = dashParts[0] + "-" + dashParts[1]
		var err error
		rcNum, err = strconv.Atoi(strings.TrimPrefix(dashParts[1], expSuffix))
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
	code += 1

	return &Version{
		Short:       short,
		Code:        code,
		GitDescribe: describe,
		GitCommit:   lines[1],
	}
})
