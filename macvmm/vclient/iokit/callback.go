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

	"k8s.io/klog/v2"
)

//export go_iokit_sleepwake_callback
func go_iokit_sleepwake_callback(refcon unsafe.Pointer, service C.io_service_t, messageType C.natural_t, messageArgument unsafe.Pointer) {
	switch messageType {
	// Always allow sleep. The purpose of this callback is just for notifying
	case C.kIOMessageCanSystemSleep:
		klog.V(1).Info("**** can sleep")
		C.IOAllowPowerChange(rootPort, C.long(uintptr(messageArgument)))
	case C.kIOMessageSystemWillSleep:
		klog.V(1).Info("**** will sleep")
		C.IOAllowPowerChange(rootPort, C.long(uintptr(messageArgument)))
	case C.kIOMessageSystemWillPowerOn:
		klog.Info("**** sync time")
		// Never block
		go func() {
			wakeChan <- time.Now()
		}()
	}
}
