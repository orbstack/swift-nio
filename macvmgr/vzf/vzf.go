package vzf

type ConsoleSpec struct {
	ReadFd  int `json:"readFd"`
	WriteFd int `json:"writeFd"`
}

type VzSpec struct {
	Cpus              int          `json:"cpus"`
	Memory            uint64       `json:"memory"`
	Kernel            string       `json:"kernel"`
	Cmdline           string       `json:"cmdline"`
	Console           *ConsoleSpec `json:"console"`
	Mtu               int          `json:"mtu"`
	MacAddressPrefix  string       `json:"macAddressPrefix"`
	NetworkVnetFd     *int         `json:"networkVnetFd"`
	NetworkNat        bool         `json:"networkNat"`
	NetworkPairFd     *int         `json:"networkPairFd"`
	NetworkHostBridge bool         `json:"networkHostBridge"`
	Rng               bool         `json:"rng"`
	DiskRootfs        string       `json:"diskRootfs,omitempty"`
	DiskData          string       `json:"diskData,omitempty"`
	DiskSwap          string       `json:"diskSwap,omitempty"`
	Balloon           bool         `json:"balloon"`
	Vsock             bool         `json:"vsock"`
	Virtiofs          bool         `json:"virtiofs"`
	Rosetta           bool         `json:"rosetta"`
	Sound             bool         `json:"sound"`
}
