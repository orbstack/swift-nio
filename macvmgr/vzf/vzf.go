package vzf

type ConsoleSpec struct {
	ReadFd  int `json:"readFd"`
	WriteFd int `json:"writeFd"`
}

type VzSpec struct {
	Cpus             int          `json:"cpus"`
	Memory           uint64       `json:"memory"`
	Kernel           string       `json:"kernel"`
	Cmdline          string       `json:"cmdline"`
	Console          *ConsoleSpec `json:"console"`
	Mtu              int          `json:"mtu"`
	MacAddressPrefix string       `json:"macAddressPrefix"`
	NetworkNat       bool         `json:"networkNat"`
	NetworkFds       []int        `json:"networkFds"`
	Rng              bool         `json:"rng"`
	DiskRootfs       string       `json:"diskRootfs,omitempty"`
	DiskData         string       `json:"diskData,omitempty"`
	DiskSwap         string       `json:"diskSwap,omitempty"`
	Balloon          bool         `json:"balloon"`
	Vsock            bool         `json:"vsock"`
	Virtiofs         bool         `json:"virtiofs"`
	Rosetta          bool         `json:"rosetta"`
	Sound            bool         `json:"sound"`
}

type BridgeNetworkConfig struct {
	GuestFd         int  `json:"guestFd"`
	ShouldReadGuest bool `json:"shouldReadGuest"`

	UUID            string   `json:"uuid"`
	Ip4Address      string   `json:"ip4Address,omitempty"`
	Ip4Mask         string   `json:"ip4Mask"`
	Ip6Address      string   `json:"ip6Address,omitempty"`
	HostOverrideMAC []uint16 `json:"hostOverrideMac,omitempty"`

	MaxLinkMTU int `json:"maxLinkMtu"`
}

type VlanRouterConfig struct {
	GuestFd   int      `json:"guestFd"`
	MACPrefix []uint16 `json:"macPrefix"`
}
