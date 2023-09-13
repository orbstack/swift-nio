package drm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/drm/ioreg"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	//saltDeviceID  = "0b196157-7644-4a11-80fe-20f42cf7f69a"
	//saltInstallID = "f3f58b2c-c40f-4489-bbef-3f69020b965f"
	//saltClientID  = "d67d0236-dcb9-48d2-854d-aed7c5a02279"

	// bin versions
	saltDeviceIDBin  = "\x0b\x19\x61\x57\x76\x44\x4a\x11\x80\xfe\x20\xf4\x2c\xf7\xf6\x9a"
	saltInstallIDBin = "\xf3\xf5\x8b\x2c\xc4\x0f\x44\x89\xbb\xef\x3f\x69\x02\x0b\x96\x5f"
	saltClientIDBin  = "\xd6\x7d\x02\x36\xdc\xb9\x48\xd2\x85\x4d\xae\xd7\xc5\xa0\x22\x79"
)

var (
	uuidRegexp = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func hashPieces(salt string, pieces ...string) string {
	// hash = sha256(salt + pieces...)
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte{0})
	for _, piece := range pieces {
		h.Write([]byte(piece))
		h.Write([]byte{0})
	}

	hash := h.Sum(nil)
	return hex.EncodeToString(hash)
}

var ReadInstallID = sync.OnceValues(func() (string, error) {
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
			return "", err
		}
	}

	return installID, nil
})

func deriveIdentifiers() (*drmtypes.Identifiers, error) {
	ids := &drmtypes.Identifiers{}

	// device id = (hardware uuid, serial number, mac address)
	hwUuid, err := ioreg.GetPlatformUUID()
	if err != nil {
		logError(err)
	}

	serial, err := ioreg.GetSerialNumber()
	if err != nil {
		logError(err)
	}

	mac, err := ioreg.GetMacAddress()
	if err != nil {
		logError(err)
	}

	ids.DeviceID = hashPieces(saltDeviceIDBin, hwUuid, serial, mac)

	// install id = from file
	installID, err := ReadInstallID()
	if err != nil {
		logError(err)
	}

	ids.InstallID = hashPieces(saltInstallIDBin, installID)

	// client id = (birth time of home directory)
	var stat unix.Stat_t
	err = unix.Stat(conf.HomeDir(), &stat)
	if err != nil {
		logError(err)
	}

	ids.ClientID = hashPieces(saltClientIDBin, fmt.Sprintf("%d", stat.Btim.Nano()))

	/*
		fmt.Println("calc ids")
		fmt.Println("hwuuid =", hwUuid)
		fmt.Println("serial =", serial)
		fmt.Println("mac =", mac)
		fmt.Println("install id data =", installID)
		fmt.Println("birth time =", stat.Btim.Nano())
		fmt.Println("device id =", ids.DeviceID)
		fmt.Println("install id =", ids.InstallID)
		fmt.Println("client id =", ids.ClientID)
	*/

	return ids, nil
}

func logError(err error) {
	if verboseDebug {
		logrus.WithError(err).Error("drm error")
	}
}
