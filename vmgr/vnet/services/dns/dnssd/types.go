package dnssd

import "errors"

type QueryAnswer struct {
	Name  string
	Type  uint16
	Class uint16
	Data  []byte
	TTL   uint32
}

var (
	ErrUnknown                   = errors.New("unknown error")
	ErrNoSuchName                = errors.New("no such name")
	ErrNoMemory                  = errors.New("no memory")
	ErrBadParam                  = errors.New("bad param")
	ErrBadReference              = errors.New("bad reference")
	ErrBadState                  = errors.New("bad state")
	ErrBadFlags                  = errors.New("bad flags")
	ErrUnsupported               = errors.New("unsupported")
	ErrNotInitialized            = errors.New("not initialized")
	ErrAlreadyRegistered         = errors.New("already registered")
	ErrNameConflict              = errors.New("name conflict")
	ErrInvalid                   = errors.New("invalid")
	ErrFirewall                  = errors.New("firewall")
	ErrIncompatible              = errors.New("incompatible")
	ErrBadInterfaceIndex         = errors.New("bad interface index")
	ErrRefused                   = errors.New("refused")
	ErrNoSuchRecord              = errors.New("no such record")
	ErrNoAuth                    = errors.New("no auth")
	ErrNoSuchKey                 = errors.New("no such key")
	ErrNATTraversal              = errors.New("NAT traversal")
	ErrDoubleNAT                 = errors.New("double NAT")
	ErrBadTime                   = errors.New("bad time")
	ErrBadSig                    = errors.New("bad sig")
	ErrBadKey                    = errors.New("bad key")
	ErrTransient                 = errors.New("transient")
	ErrServiceNotRunning         = errors.New("service not running")
	ErrNATPortMappingUnsupported = errors.New("NAT port mapping unsupported")
	ErrNATPortMappingDisabled    = errors.New("NAT port mapping disabled")
	ErrNoRouter                  = errors.New("no router")
	ErrPollingMode               = errors.New("polling mode")
	ErrTimeout                   = errors.New("timeout")
	ErrDefunctConnection         = errors.New("defunct connection")
	ErrPolicyDenied              = errors.New("policy denied")
	ErrNotPermitted              = errors.New("not permitted")
)
