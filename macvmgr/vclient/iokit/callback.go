//go:build darwin

package iokit

/*
#include <mach/mach_port.h>
#include <mach/mach_interface.h>
#include <mach/mach_init.h>

#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>
*/
import "C"
import (
	"time"
	"unsafe"

	"github.com/sirupsen/logrus"
)

//export go_iokit_sleepwake_callback
func go_iokit_sleepwake_callback(refcon unsafe.Pointer, service C.io_service_t, messageType C.natural_t, messageArgument unsafe.Pointer) {
	switch messageType {
	// Always allow sleep. The purpose of this callback is just for notifying
	case C.kIOMessageCanSystemSleep:
		logrus.Debug("can sleep")
		C.IOAllowPowerChange(rootPort, C.long(uintptr(messageArgument)))
	case C.kIOMessageSystemWillSleep:
		logrus.Debug("will sleep")
		// Never block
		go func() {
			sleepChan <- time.Now()
		}()
		C.IOAllowPowerChange(rootPort, C.long(uintptr(messageArgument)))
	case C.kIOMessageSystemWillPowerOn:
		logrus.Debug("power on - sync time")
		// Never block
		go func() {
			wakeChan <- time.Now()
		}()
	}
}
