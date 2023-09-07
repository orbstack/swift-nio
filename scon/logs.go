package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
)

func (c *Container) readLogsLocked(logType types.LogType) (string, error) {
	var path string
	switch logType {
	case types.LogRuntime:
		path = c.logPath()
	case types.LogConsole:
		path = c.logPath() + "-console"
	}

	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"path":      path,
	}).Debug("reading log")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("machine '%s' has no logs of type %s", c.Name, logType)
		} else {
			return "", err
		}
	}

	// obfuscate lxc
	logStr := string(data)
	if logType == types.LogRuntime {
		logStr = strings.Replace(logStr, "lxc", "ctr", -1)
	}

	return logStr, nil
}

func (c *Container) GetLogs(logType types.LogType) (string, error) {
	// no lock needed
	return c.readLogsLocked(logType)
}
