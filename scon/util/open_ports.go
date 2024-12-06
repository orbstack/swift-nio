package util

import (
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

func ParseNetTcpPorts(contents string, openPorts map[uint16]struct{}) error {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		if strings.HasSuffix(fields[0], ":") {
			laddrField := fields[1]
			portPart := laddrField[strings.LastIndex(laddrField, ":")+1:]
			port, err := strconv.ParseUint(portPart, 16, 16)
			logrus.Debugf("got port: %v", port)
			if err != nil {
				logrus.WithError(err).Debug("failed to parse port")
				continue
			}

			openPorts[uint16(port)] = struct{}{}
		}
	}

	return nil
}
