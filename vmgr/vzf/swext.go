package vzf

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

type BridgeNetworkConfig struct {
	GuestFd         int  `json:"guestFd"`
	ShouldReadGuest bool `json:"shouldReadGuest"`

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
	GuestFd           int      `json:"guestFd"`
	MACPrefix         []uint16 `json:"macPrefix"`
	MaxVlanInterfaces int      `json:"maxVlanInterfaces"`
}
