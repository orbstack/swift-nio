package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/sirupsen/logrus"
)

func (c *Container) GetLogs(logType types.LogType) (string, error) {
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
	}).Debug("reading logs")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("machine '%s' has no logs of type %s", c.Name, logType)
		} else {
			return "", err
		}
	}

	return string(data), nil
}
