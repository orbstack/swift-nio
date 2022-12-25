package dnssd

/*
#cgo CFLAGS: -Wall
#include <dns_sd.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

//export go_dnssd_callback
func go_dnssd_callback(context uint64, flags C.DNSServiceFlags, interfaceIndex C.uint32_t,
	errorCode C.DNSServiceErrorType, fullname *C.char, rrtype C.uint16_t,
	rrclass C.uint16_t, rdlen C.uint16_t, rdata unsafe.Pointer, ttl C.uint32_t) {
	fmt.Printf("go_dnssd_callback(context=%v, flags=%v, interfaceIndex=%v, errorCode=%v, fullname=%v, rrtype=%v, rrclass=%v, rdlen=%v, rdata=%v, ttl=%v)\n", context, flags, interfaceIndex, errorCode, C.GoString(fullname), rrtype, rrclass, rdlen, rdata, ttl)

	queryMapMu.RLock()
	query, ok := queryMap[context]
	queryMapMu.RUnlock()
	if !ok {
		fmt.Printf("go_dnssd_callback: no query for context %d\n", context)
		return
	}

	if errorCode != 0 {
		fmt.Printf("go_dnssd_callback: error %d\n", errorCode)
		query.err = mapError(int(errorCode))
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

	if flags&C.kDNSServiceFlagsMoreComing != 0 {
		fmt.Printf("go_dnssd_callback: MoreComing\n")
	} else {
		C.DNSServiceRefDeallocate(query.ref)
	}
	fmt.Println("go_dnssd_callback: done")
}
