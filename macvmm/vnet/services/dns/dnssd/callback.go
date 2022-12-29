package dnssd

/*
#cgo CFLAGS: -Wall
#include <dns_sd.h>
*/
import "C"
import (
	"unsafe"

	"k8s.io/klog/v2"
)

//export go_dnssd_callback
func go_dnssd_callback(context uint64, flags C.DNSServiceFlags, interfaceIndex C.uint32_t,
	errorCode C.DNSServiceErrorType, fullname *C.char, rrtype C.uint16_t,
	rrclass C.uint16_t, rdlen C.uint16_t, rdata unsafe.Pointer, ttl C.uint32_t) {

	queryMapMu.RLock()
	query, ok := queryMap[context]
	queryMapMu.RUnlock()
	if !ok {
		klog.Error("no dns query for context", context)
		return
	}

	if errorCode != 0 {
		klog.Error("dnssd error %d", errorCode)
		query.err = mapError(int(errorCode))
		query.done = true
		C.DNSServiceRefDeallocate(query.ref)
		return
	}

	answer := QueryAnswer{
		Name:  C.GoString(fullname),
		Type:  uint16(rrtype),
		Class: uint16(rrclass),
		Data:  C.GoBytes(rdata, C.int(rdlen)),
		TTL:   uint32(ttl),
	}
	query.answers = append(query.answers, answer)

	if flags&C.kDNSServiceFlagsMoreComing == 0 {
		query.done = true
		C.DNSServiceRefDeallocate(query.ref)
	}
}
