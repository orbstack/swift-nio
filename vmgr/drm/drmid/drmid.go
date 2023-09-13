package drmid

import (
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
)

var (
	uuidRegexp = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

var ReadInstallID = sync.OnceValue(func() string {
	installIDData, err := os.ReadFile(conf.InstallIDFile())
	var installID string
	if err == nil {
		installID = strings.TrimSpace(string(installIDData))
	}
	if err != nil || !uuidRegexp.MatchString(installID) {
		// write a new one
		installID = uuid.NewString()
		err = os.WriteFile(conf.InstallIDFile(), []byte(installID), 0644)
		if err != nil {
			// continue with a random one if not readable
			logrus.WithError(err).Error("failed to write install id")
		}
	}

	return installID
})

func NewInstallID() string {
	return uuid.NewString()
}
