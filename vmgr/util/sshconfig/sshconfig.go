package sshconfig

import (
	"strings"

	"github.com/orbstack/macvirt/vmgr/util"
)

func ReadForHost(host string) (map[string]string, error) {
	// print k/v pairs for host
	rawOut, err := util.Run("ssh", "-G", host)
	if err != nil {
		return nil, err
	}

	vals := make(map[string]string)
	for _, line := range strings.Split(rawOut, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		vals[k] = v
	}

	return vals, nil
}

func ReadKeyForHost(host, key string) (string, error) {
	vals, err := ReadForHost(host)
	if err != nil {
		return "", err
	}

	return vals[strings.ToLower(key)], nil
}
