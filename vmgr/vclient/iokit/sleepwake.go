//go:build darwin

package iokit

/*
#cgo CFLAGS: -O2
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit

#include <mach/mach_port.h>
#include <mach/mach_interface.h>
#include <mach/mach_init.h>

#include <IOKit/pwr_mgt/IOPMLib.h>
#include <IOKit/IOMessage.h>

extern void go_iokit_sleepwake_callback(void* refcon, io_service_t service, natural_t messageType, void* messageArgument);
void iokit_sleepwake_callback(void* refcon, io_service_t service, natural_t messageType, void* messageArgument) {
	go_iokit_sleepwake_callback(refcon, service, messageType, messageArgument);
}

io_connect_t io_register_for_system_power(void* refcon, IONotificationPortRef* notifyPortRef, io_object_t* notifierObject) {
	return IORegisterForSystemPower(refcon, notifyPortRef, iokit_sleepwake_callback, notifierObject);
}
*/
import "C"
import (
	"errors"
	"runtime"
	"time"
	"unsafe"

	"github.com/orbstack/macvirt/vmgr/drm/timex"
	swext "github.com/orbstack/macvirt/vmgr/swext"
	"github.com/sirupsen/logrus"
)

var (
	shouldRun = false
	sleepChan = make(chan time.Time)
	wakeChan  = make(chan time.Time)
	rootPort  C.io_connect_t
	isAsleep  = false

	LastSleepTime *timex.MonoSleepTime
	LastWakeTime  *timex.MonoSleepTime
)

type SleepWakeMonitor struct {
	SleepChan chan time.Time
	WakeChan  chan time.Time
}

func runLoop() {
	for shouldRun {
		C.CFRunLoopRun()
	}
}

func MonitorSleepWake() (*SleepWakeMonitor, error) {
	if shouldRun {
		return nil, errors.New("already started")
	}

	refCon := unsafe.Pointer(nil)
	var notifyPortRef C.IONotificationPortRef
	var notifierObject C.io_object_t
	rootPort = C.io_register_for_system_power(refCon, &notifyPortRef, &notifierObject)
	if rootPort == 0 {
		return nil, errors.New("IORegisterForSystemPower failed")
	}

	shouldRun = true
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		C.CFRunLoopAddSource(C.CFRunLoopGetCurrent(), C.IONotificationPortGetRunLoopSource(notifyPortRef), C.kCFRunLoopDefaultMode)

		// also add SC proxy settings notifier to runloop
		err := swext.ProxyMonitorChangesOnRunLoop()
		if err != nil {
			logrus.WithError(err).Error("failed to watch proxy settings")
		}

		runLoop()
	}()

	return &SleepWakeMonitor{
		SleepChan: sleepChan,
		WakeChan:  wakeChan,
	}, nil
}

func IsAsleep() bool {
	return isAsleep
}

func SleepOrWakeWithin(duration time.Duration) bool {
	sleepTime := LastSleepTime
	if sleepTime != nil && timex.SinceMonoSleep(*sleepTime) < duration {
		return true
	}
	wakeTime := LastWakeTime
	if wakeTime != nil && timex.SinceMonoSleep(*wakeTime) < duration {
		return true
	}
	return false
}
