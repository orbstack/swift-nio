package vzf

import "golang.org/x/sys/unix"

var (
	SwextProxyChangesChan = make(chan struct{}, 1)

	SwextFseventsKrpcEventsChan = make(chan []byte)

	SwextNetPathChangesChan = make(chan struct{}, 1)
)

type SwextProxySettings struct {
	HTTPEnable   bool   `json:"httpEnable"`
	HTTPProxy    string `json:"httpProxy,omitempty"`
	HTTPPort     int    `json:"httpPort,omitempty"`
	HTTPUser     string `json:"httpUser,omitempty"`
	HTTPPassword string `json:"httpPassword,omitempty"`

	HTTPSEnable   bool   `json:"httpsEnable"`
	HTTPSProxy    string `json:"httpsProxy,omitempty"`
	HTTPSPort     int    `json:"httpsPort,omitempty"`
	HTTPSUser     string `json:"httpsUser,omitempty"`
	HTTPSPassword string `json:"httpsPassword,omitempty"`

	SOCKSEnable   bool   `json:"socksEnable"`
	SOCKSProxy    string `json:"socksProxy,omitempty"`
	SOCKSPort     int    `json:"socksPort,omitempty"`
	SOCKSUser     string `json:"socksUser,omitempty"`
	SOCKSPassword string `json:"socksPassword,omitempty"`

	ExceptionsList []string `json:"exceptionsList,omitempty"`
}

type SwextUserSettings struct {
	ShowMenubarExtra    bool   `json:"showMenubarExtra"`
	UpdatesOptinChannel string `json:"updatesOptinChannel"`
}

// can't use anonymous struct: an `_0` field would be private, so we need a json tag
type netHandleRsvm struct {
	// Swift Codable uses "_0", "_1", etc. for unlabeled associated values
	Value uintptr `json:"_0"`
}

type netHandleFd struct {
	Value int `json:"_0"`
}

// "algebraic" enum compatible with Swift Codable
type NetHandle struct {
	// zero is a valid handle value, so this has to be a pointer
	Rsvm *netHandleRsvm `json:"rsvm,omitempty"`
	Fd   *netHandleFd   `json:"fd,omitempty"`
}

func NetHandleFromFd(fd int) NetHandle {
	return NetHandle{Fd: &netHandleFd{Value: fd}}
}

func NetHandleFromRsvmHandle(handle uintptr) NetHandle {
	return NetHandle{Rsvm: &netHandleRsvm{Value: handle}}
}

func (h NetHandle) Close() error {
	if h.Fd != nil {
		return unix.Close(h.Fd.Value)
	}
	return nil
}

type BridgeNetworkConfig struct {
	GuestHandle     NetHandle `json:"guestHandle"`
	GuestSconHandle NetHandle `json:"guestSconHandle"`
	// is BridgeNetwork directly responsible for reading from guest (i.e. owned), or is it VlanRouter?
	OwnsGuestReader bool `json:"ownsGuestReader"`

	UUID       string `json:"uuid"`
	Ip4Address string `json:"ip4Address,omitempty"`
	Ip4Mask    string `json:"ip4Mask"`
	Ip6Address string `json:"ip6Address,omitempty"`

	HostOverrideMAC []uint16 `json:"hostOverrideMac,omitempty"`
	GuestMAC        []uint16 `json:"guestMac,omitempty"`
	NDPReplyPrefix  []uint16 `json:"ndpReplyPrefix,omitempty"`
	AllowMulticast  bool     `json:"allowMulticast"`

	MaxLinkMTU int `json:"maxLinkMtu"`
}

type VlanRouterConfig struct {
	GuestHandle       NetHandle `json:"guestHandle"`
	MACPrefix         []uint16  `json:"macPrefix"`
	MaxVlanInterfaces int       `json:"maxVlanInterfaces"`
}
