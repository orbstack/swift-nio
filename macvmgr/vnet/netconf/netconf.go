package netconf

const (
	Subnet4       = "198.19.248"
	GatewayIP4    = Subnet4 + ".1"
	GuestIP4      = Subnet4 + ".2"
	ServicesIP4   = Subnet4 + ".200"
	SecureSvcIP4  = Subnet4 + ".201"
	ExtHostNatIP4 = Subnet4 + ".253"
	HostNatIP4    = Subnet4 + ".254"

	Subnet6 = "fd00:96dc:7096:1df0:"
	// hack: because we don't implement NDP, we need to use a different subnet for anything that's not guest or gateway
	SubnetExt6    = "fd00:96dc:7096:1df1:"
	GatewayIP6    = Subnet6 + ":1"
	GuestIP6      = Subnet6 + ":2"
	ExtHostNatIP6 = SubnetExt6 + ":253"
	HostNatIP6    = SubnetExt6 + ":254"
)

const (
	SconSubnet4   = "198.19.249"
	SconDockerIP4 = SconSubnet4 + ".2"

	SconSubnet6   = "fd00:96dc:7096:1d00:"
	SconDockerIP6 = SconSubnet6 + ":2"
)

// static ARP/neighbors to save CPU
const (
	GuestMACPrefix = "86:6c:f1:2e:9e"
	GuestMACVnet   = GuestMACPrefix + ":01"

	GatewayMAC = "24:d2:f4:58:34:d7"
)
