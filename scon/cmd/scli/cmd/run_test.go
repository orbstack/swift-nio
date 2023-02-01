package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseArgs(t *testing.T) {
	tests := map[string]struct {
		help    bool
		shell   bool
		path    bool
		machine string
		user    string
		rem     string
		err     bool
	}{
		"":                                {rem: ""},
		"-h":                              {help: true},
		"-s":                              {shell: true},
		"-p":                              {path: true},
		"-m":                              {err: true},
		"-u":                              {err: true},
		"-m foo":                          {machine: "foo"},
		"-u foo":                          {user: "foo"},
		"-m foo -u bar":                   {machine: "foo", user: "bar"},
		"-m foo -u bar -s":                {machine: "foo", user: "bar", shell: true},
		"-m foo -u bar -s -p":             {machine: "foo", user: "bar", shell: true, path: true},
		"-m foo -u bar -s -p -h":          {machine: "foo", user: "bar", shell: true, path: true, help: true},
		"-m foo -u bar -s -p -h -x":       {err: true},
		"-m foo -u bar -s -p -h -x -y":    {err: true},
		"-m foo -u bar -s -p -h -x -y -z": {err: true},
		"-m foo --shell --path=true -m bar uname -a --shell":             {machine: "bar", shell: true, path: true, rem: "uname -a --shell"},
		"-m foo --shell --path=true -m bar uname -a --shell -h":          {machine: "bar", shell: true, path: true, rem: "uname -a --shell -h"},
		"-machine=foo --shell --path=true -machine=bar uname -a --shell": {machine: "bar", shell: true, path: true, rem: "uname -a --shell"},
		"--shell --path=true -machine=bar uname -a --shell":              {machine: "bar", shell: true, path: true, rem: "uname -a --shell"},
		// some variety
		"-p -m foo -u bar -s -h cmd -a -h": {machine: "foo", user: "bar", shell: true, path: true, help: true, rem: "cmd -a -h"},
		"cmd -a -h":                        {rem: "cmd -a -h"},
		"cmd -p":                           {rem: "cmd -p"},
		"--cmd":                            {err: true},

		// more by ChatGPT
		"ls -l":                                 {rem: "ls -l"},
		"-h ls -l":                              {help: true, rem: "ls -l"},
		"-s ls -l":                              {shell: true, rem: "ls -l"},
		"-p ls -l":                              {path: true, rem: "ls -l"},
		"-m ls -l":                              {err: true},
		"-u ls -l":                              {err: true},
		"-m foo ls -l":                          {machine: "foo", rem: "ls -l"},
		"-u foo ls -l":                          {user: "foo", rem: "ls -l"},
		"-m foo -u bar ls -l":                   {machine: "foo", user: "bar", rem: "ls -l"},
		"-m foo -u bar -s ls -l":                {machine: "foo", user: "bar", shell: true, rem: "ls -l"},
		"-m foo -u bar -s -p ls -l":             {machine: "foo", user: "bar", shell: true, path: true, rem: "ls -l"},
		"-m foo -u bar -s -p -h ls -l":          {machine: "foo", user: "bar", shell: true, path: true, help: true, rem: "ls -l"},
		"-m foo -u bar -s -p -h -x ls -l":       {err: true},
		"-m foo -u bar -s -p -h -x -y ls -l":    {err: true},
		"-m foo -u bar -s -p -h -x -y -z ls -l": {err: true},
		"rm -rf /":                              {rem: "rm -rf /"},
		"-s rm -rf /":                           {shell: true, rem: "rm -rf /"},
		"-p rm -rf /":                           {path: true, rem: "rm -rf /"},
		"-m rm -rf /":                           {err: true},
		"-u rm -rf /":                           {err: true},
		"-m foo rm -rf /":                       {machine: "foo", rem: "rm -rf /"},
		"-u foo rm -rf /":                       {user: "foo", rem: "rm -rf /"},
		"-m foo -u bar rm -rf /":                {machine: "foo", user: "bar", rem: "rm -rf /"},
		"-m foo -u bar -s rm -rf /":             {machine: "foo", user: "bar", shell: true, rem: "rm -rf /"},
		"-m foo -u bar -s -p rm -rf /":          {machine: "foo", user: "bar", shell: true, path: true, rem: "rm -rf /"},
	}

	for args, test := range tests {
		t.Logf("Testing: %s", args)
		rem, err := parseRunFlags(strings.Split(args, " "))
		assert.Equal(t, test.err, err != nil, "err")
		if !test.err {
			assert.Equal(t, test.help, flagWantHelp, "help")
			assert.Equal(t, test.shell, useShell, "shell")
			assert.Equal(t, test.path, usePath, "path")
			assert.Equal(t, test.machine, flagMachine, "machine")
			assert.Equal(t, test.user, flagUser, "user")
			assert.Equal(t, test.rem, strings.Join(rem, " "), "rem")
		}

		// reset flags
		flagWantHelp = false
		useShell = false
		usePath = false
		flagMachine = ""
		flagUser = ""
	}
}
