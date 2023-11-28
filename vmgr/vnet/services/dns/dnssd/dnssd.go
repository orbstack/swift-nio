//go:build darwin

package dnssd

/*
#cgo CFLAGS: -Wall
#include <dns_sd.h>

extern void go_dnssd_callback(uint64_t context, DNSServiceFlags flags, uint32_t interfaceIndex,
		DNSServiceErrorType errorCode, const char *fullname, uint16_t rrtype,
		uint16_t rrclass, uint16_t rdlen, const void *rdata, uint32_t ttl);
void dnssd_callback(DNSServiceRef sdRef, DNSServiceFlags flags, uint32_t interfaceIndex,
		DNSServiceErrorType errorCode, const char *fullname, uint16_t rrtype,
		uint16_t rrclass, uint16_t rdlen, const void *rdata, uint32_t ttl, void *context) {
	go_dnssd_callback((uint64_t) context, flags, interfaceIndex, errorCode, (char *)fullname, rrtype, rrclass, rdlen, (void *)rdata, ttl);
}

DNSServiceErrorType start_query_record(DNSServiceRef *sdRef, DNSServiceFlags flags, uint32_t interfaceIndex, const char *fullname, uint16_t rrtype, uint16_t rrclass, uint64_t context) {
	return DNSServiceQueryRecord(sdRef, flags, interfaceIndex, fullname, rrtype, rrclass, dnssd_callback, (void*) context);
}
*/
import "C"
import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
)

// prevent loop when domain keeps resolving to same CNAME
const maxCnameRecursion = 5

var (
	queryMap   = map[uint64]*queryState{}
	queryMapMu = sync.Mutex{}

	// guarantees no overlap
	nextSeq atomic.Uint64
)

var verboseTrace = conf.Debug()

type queryState struct {
	ref     C.DNSServiceRef
	answers []QueryAnswer
	err     error
	done    bool
}

func queryOne(name string, rtype uint16) ([]QueryAnswer, error) {
	rclass := C.kDNSServiceClass_IN

	nameC := C.CString(name)
	defer C.free(unsafe.Pointer(nameC))

	var sdRef C.DNSServiceRef
	queryId := nextSeq.Add(1)
	ret := C.start_query_record(&sdRef, C.kDNSServiceFlagsTimeout|C.kDNSServiceFlagsReturnIntermediates, 0, nameC, C.ushort(rtype), C.ushort(rclass), C.uint64_t(queryId))
	if ret != C.kDNSServiceErr_NoError {
		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"ret": ret,
			}).Trace("DNSServiceQueryRecord returned error")
		}
		return nil, mapError(int(ret))
	}
	// guarantee free on error path
	defer C.DNSServiceRefDeallocate(sdRef)

	query := &queryState{
		ref: sdRef,
	}

	// homegrown cgo.Handle
	queryMapMu.Lock()
	queryMap[queryId] = query
	queryMapMu.Unlock()
	defer func() {
		queryMapMu.Lock()
		delete(queryMap, queryId)
		queryMapMu.Unlock()
	}()

	fd := int32(C.DNSServiceRefSockFD(sdRef))
	if fd < 0 {
		return nil, errors.New("invalid fd")
	}

	for {
		if query.done {
			break
		}

		ret := C.DNSServiceProcessResult(sdRef)
		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"ret": ret,
			}).Trace("DNSServiceProcessResult returned")
		}
		if ret != C.kDNSServiceErr_NoError {
			logrus.Error("DNSServiceProcessResult err", mapError(int(ret)))
			return query.answers, mapError(int(ret))
		}
	}

	return query.answers, query.err
}

func QueryRecursive(name string, rtype uint16) ([]QueryAnswer, error) {
	// Keep CNAME at the top even if we're not looking for it
	allAnswers := []QueryAnswer{}
	for i := 0; i < maxCnameRecursion; i++ {
		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"name": name,
				"type": dns.TypeToString[rtype],
			}).Trace("QueryOne")
		}
		newAnswers, err := queryOne(name, rtype)
		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"name": name,
				"type": dns.TypeToString[rtype],
				"ans":  newAnswers,
				"err":  err,
			}).Trace("QueryOne result")
		}
		allAnswers = append(allAnswers, newAnswers...)
		if err != nil {
			return allAnswers, err
		}

		// Recurse if we only got CNAMEs for a non-CNAME query
		// Happens when macOS doesn't have A/AAAA cached
		// If it's cached, we don't need to recurse
		if len(newAnswers) > 0 && rtype != C.kDNSServiceType_CNAME {
			for _, answer := range newAnswers {
				if answer.Type == C.kDNSServiceType_CNAME {
					// Got a CNAME, so use this name for the next recursion
					// (parse target)
					rr, _, err := dns.UnpackRRWithHeader(dns.RR_Header{
						Name:     answer.Name,
						Rrtype:   answer.Type,
						Class:    answer.Class,
						Ttl:      answer.TTL,
						Rdlength: uint16(len(answer.Data)),
					}, answer.Data, 0)
					if err != nil {
						return nil, err
					}

					name = string(rr.(*dns.CNAME).Target)
				} else {
					// We got a non-CNAME answer, so we're done
					return allAnswers, nil
				}
			}
		} else {
			return allAnswers, nil
		}
	}

	return allAnswers, nil
}

func validateType(rtype uint16) bool {
	switch rtype {
	case C.kDNSServiceType_A: /* Host address. */
	case C.kDNSServiceType_NS: /* Authoritative server. */
	case C.kDNSServiceType_MD: /* Mail destination. */
	case C.kDNSServiceType_MF: /* Mail forwarder. */
	case C.kDNSServiceType_CNAME: /* Canonical name. */
	case C.kDNSServiceType_SOA: /* Start of authority zone. */
	case C.kDNSServiceType_MB: /* Mailbox domain name. */
	case C.kDNSServiceType_MG: /* Mail group member. */
	case C.kDNSServiceType_MR: /* Mail rename name. */
	case C.kDNSServiceType_NULL: /* Null resource record. */
	case C.kDNSServiceType_WKS: /* Well known service. */
	case C.kDNSServiceType_PTR: /* Domain name pointer. */
	case C.kDNSServiceType_HINFO: /* Host information. */
	case C.kDNSServiceType_MINFO: /* Mailbox information. */
	case C.kDNSServiceType_MX: /* Mail routing information. */
	case C.kDNSServiceType_TXT: /* One or more text strings (NOT "zero or more..."). */
	case C.kDNSServiceType_RP: /* Responsible person. */
	case C.kDNSServiceType_AFSDB: /* AFS cell database. */
	case C.kDNSServiceType_X25: /* X_25 calling address. */
	case C.kDNSServiceType_ISDN: /* ISDN calling address. */
	case C.kDNSServiceType_RT: /* Router. */
	case C.kDNSServiceType_NSAP: /* NSAP address. */
	case C.kDNSServiceType_NSAP_PTR: /* Reverse NSAP lookup (deprecated). */
	case C.kDNSServiceType_SIG: /* Security signature. */
	case C.kDNSServiceType_KEY: /* Security key. */
	case C.kDNSServiceType_PX: /* X.400 mail mapping. */
	case C.kDNSServiceType_GPOS: /* Geographical position (withdrawn). */
	case C.kDNSServiceType_AAAA: /* IPv6 Address. */
	case C.kDNSServiceType_LOC: /* Location Information. */
	case C.kDNSServiceType_NXT: /* Next domain (security). */
	case C.kDNSServiceType_EID: /* Endpoint identifier. */
	case C.kDNSServiceType_NIMLOC: /* Nimrod Locator. */
	case C.kDNSServiceType_SRV: /* Server Selection. */
	case C.kDNSServiceType_ATMA: /* ATM Address */
	case C.kDNSServiceType_NAPTR: /* Naming Authority PoinTeR */
	case C.kDNSServiceType_KX: /* Key Exchange */
	case C.kDNSServiceType_CERT: /* Certification record */
	case C.kDNSServiceType_A6: /* IPv6 Address (deprecated) */
	case C.kDNSServiceType_DNAME: /* Non-terminal DNAME (for IPv6) */
	case C.kDNSServiceType_SINK: /* Kitchen sink (experimental) */
	case C.kDNSServiceType_OPT: /* EDNS0 option (meta-RR) */
	case C.kDNSServiceType_APL: /* Address Prefix List */
	case C.kDNSServiceType_DS: /* Delegation Signer */
	case C.kDNSServiceType_SSHFP: /* SSH Key Fingerprint */
	case C.kDNSServiceType_IPSECKEY: /* IPSECKEY */
	case C.kDNSServiceType_RRSIG: /* RRSIG */
	case C.kDNSServiceType_NSEC: /* Denial of Existence */
	case C.kDNSServiceType_DNSKEY: /* DNSKEY */
	case C.kDNSServiceType_DHCID: /* DHCP Client Identifier */
	case C.kDNSServiceType_NSEC3: /* Hashed Authenticated Denial of Existence */
	case C.kDNSServiceType_NSEC3PARAM: /* Hashed Authenticated Denial of Existence */

	case C.kDNSServiceType_HIP: /* Host Identity Protocol */

	case C.kDNSServiceType_SVCB: /* Service Binding. */
	case C.kDNSServiceType_HTTPS: /* HTTPS Service Binding. */

	case C.kDNSServiceType_SPF: /* Sender Policy Framework for E-Mail */
	case C.kDNSServiceType_UINFO: /* IANA-Reserved */
	case C.kDNSServiceType_UID: /* IANA-Reserved */
	case C.kDNSServiceType_GID: /* IANA-Reserved */
	case C.kDNSServiceType_UNSPEC: /* IANA-Reserved */

	case C.kDNSServiceType_TKEY: /* Transaction key */
	case C.kDNSServiceType_TSIG: /* Transaction signature. */
	case C.kDNSServiceType_IXFR: /* Incremental zone transfer. */
	case C.kDNSServiceType_AXFR: /* Transfer zone of authority. */
	case C.kDNSServiceType_MAILB: /* Transfer mailbox records. */
	case C.kDNSServiceType_MAILA: /* Transfer mail agent records. */
	case C.kDNSServiceType_ANY: /* Wildcard match. */
	default:
		return false
	}

	return true
}

func mapError(ret int) error {
	switch ret {
	case C.kDNSServiceErr_NoError:
		return nil
	case C.kDNSServiceErr_Unknown: /* 0xFFFE FFFF */
		return ErrUnknown
	case C.kDNSServiceErr_NoSuchName:
		return ErrNoSuchName
	case C.kDNSServiceErr_NoMemory:
		return ErrNoMemory
	case C.kDNSServiceErr_BadParam:
		return ErrBadParam
	case C.kDNSServiceErr_BadReference:
		return ErrBadReference
	case C.kDNSServiceErr_BadState:
		return ErrBadState
	case C.kDNSServiceErr_BadFlags:
		return ErrBadFlags
	case C.kDNSServiceErr_Unsupported:
		return ErrUnsupported
	case C.kDNSServiceErr_NotInitialized:
		return ErrNotInitialized
	case C.kDNSServiceErr_AlreadyRegistered:
		return ErrAlreadyRegistered
	case C.kDNSServiceErr_NameConflict:
		return ErrNameConflict
	case C.kDNSServiceErr_Invalid:
		return ErrInvalid
	case C.kDNSServiceErr_Firewall:
		return ErrFirewall
	case C.kDNSServiceErr_Incompatible: /* client library incompatible with daemon */
		return ErrIncompatible
	case C.kDNSServiceErr_BadInterfaceIndex:
		return ErrBadInterfaceIndex
	case C.kDNSServiceErr_Refused:
		return ErrRefused
	case C.kDNSServiceErr_NoSuchRecord:
		return ErrNoSuchRecord
	case C.kDNSServiceErr_NoAuth:
		return ErrNoAuth
	case C.kDNSServiceErr_NoSuchKey:
		return ErrNoSuchKey
	case C.kDNSServiceErr_NATTraversal:
		return ErrNATTraversal
	case C.kDNSServiceErr_DoubleNAT:
		return ErrDoubleNAT
	case C.kDNSServiceErr_BadTime: /* Codes up to here existed in Tiger */
		return ErrBadTime
	case C.kDNSServiceErr_BadSig:
		return ErrBadSig
	case C.kDNSServiceErr_BadKey:
		return ErrBadKey
	case C.kDNSServiceErr_Transient:
		return ErrTransient
	case C.kDNSServiceErr_ServiceNotRunning: /* Background daemon not running */
		return ErrServiceNotRunning
	case C.kDNSServiceErr_NATPortMappingUnsupported: /* NAT doesn't support PCP NAT-PMP or UPnP */
		return ErrNATPortMappingUnsupported
	case C.kDNSServiceErr_NATPortMappingDisabled: /* NAT supports PCP NAT-PMP or UPnP but it's disabled by the administrator */
		return ErrNATPortMappingDisabled
	case C.kDNSServiceErr_NoRouter: /* No router currently configured (probably no network connectivity) */
		return ErrNoRouter
	case C.kDNSServiceErr_PollingMode:
		return ErrPollingMode
	case C.kDNSServiceErr_Timeout:
		return ErrTimeout
	case C.kDNSServiceErr_DefunctConnection: /* Connection to daemon returned a SO_ISDEFUNCT error result */
		return ErrDefunctConnection
	case C.kDNSServiceErr_PolicyDenied:
		return ErrPolicyDenied
	case C.kDNSServiceErr_NotPermitted:
		return ErrNotPermitted
	default:
		return fmt.Errorf("unknown error: %d", ret)
	}
}
