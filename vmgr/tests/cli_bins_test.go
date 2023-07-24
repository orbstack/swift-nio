package tests

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

var (
	// via Rosetta
	cliArchs = []string{"arm64", "amd64"}
)

func readExpectedBinVersion(t *testing.T, prog string) string {
	t.Helper()

	scriptBytes, err := os.ReadFile("../../bins/download-bins.sh")
	checkT(t, err)

	for _, line := range strings.Split(string(scriptBytes), "\n") {
		if strings.HasPrefix(line, prog+"_VERSION") {
			return strings.Split(line, "=")[1]
		}
	}

	t.Fatalf("could not find %s in download-bins.sh", prog)
	return ""
}

type binVersionTest struct {
	ScriptProg string
	BinName    string
	Args       []string
}

func TestCliBinVersion(t *testing.T) {
	t.Parallel()

	tests := []binVersionTest{
		{
			ScriptProg: "DOCKER",
			BinName:    "docker",
			Args:       []string{"--version"},
		},
		{
			ScriptProg: "BUILDX",
			BinName:    "docker-buildx",
			Args:       []string{"version"},
		},
		{
			ScriptProg: "COMPOSE",
			BinName:    "docker-compose",
			Args:       []string{"version"},
		},
		{
			ScriptProg: "CREDENTIAL",
			BinName:    "docker-credential-osxkeychain",
			Args:       []string{"version"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.ScriptProg, func(t *testing.T) {
			t.Parallel()

			for _, arch := range cliArchs {
				arch := arch
				t.Run(arch, func(t *testing.T) {
					t.Parallel()

					// get version from download-bins.sh
					expectedVersion := readExpectedBinVersion(t, test.ScriptProg)

					// get version from bin
					binPath := fmt.Sprintf("../../bins/out/%s/%s", arch, test.BinName)
					combinedArgs := append([]string{binPath}, test.Args...)
					versionOut, err := util.Run(combinedArgs...)
					checkT(t, err)

					// compare
					matched, err := regexp.Match(`.*\bv?`+regexp.QuoteMeta(expectedVersion)+`\b.*`, []byte(versionOut))
					if err != nil {
						t.Fatal(err)
					}
					if !matched {
						t.Fatalf("expected version %s, got %s", expectedVersion, versionOut)
					}
				})
			}
		})
	}
}
