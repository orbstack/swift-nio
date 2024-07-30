package vnet

import (
	"time"

	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/log"
)

// gvisor logger to forward logs to logrus
type gvisorLogger struct{}

func (g gvisorLogger) Emit(depth int, level log.Level, timestamp time.Time, format string, args ...any) {
	var logrusLevel logrus.Level
	switch level {
	case log.Debug:
		logrusLevel = logrus.DebugLevel
	case log.Info:
		logrusLevel = logrus.InfoLevel
	case log.Warning:
		logrusLevel = logrus.WarnLevel
	}

	logrus.StandardLogger().WithTime(timestamp).Logf(logrusLevel, "[GV] "+format, args...)
}
