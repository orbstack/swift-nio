package netconf

const (
	Subnet4       = "198.19.248"
	GatewayIP4    = Subnet4 + ".1"
	GuestIP4      = Subnet4 + ".2"
	ServicesIP4   = Subnet4 + ".200"
	SecureSvcIP4  = Subnet4 + ".201"
	ExtHostNatIP4 = Subnet4 + ".253"
	HostNatIP4    = Subnet4 + ".254"

	Subnet6 = "fd07:b51a:cc66:00f0:"
	// hack: because we don't implement NDP, we need to use a different subnet for anything that's not guest or gateway
	SubnetExt6    = "fd07:b51a:cc66:00f1:"
	GatewayIP6    = Subnet6 + ":1"
	GuestIP6      = Subnet6 + ":2"
	ExtHostNatIP6 = SubnetExt6 + ":253"
	HostNatIP6    = SubnetExt6 + ":254"
)

const (
	SconSubnet4       = "198.19.249"
	SconSubnet4CIDR   = SconSubnet4 + ".0/24"
	SconGatewayIP4    = SconSubnet4 + ".1"
	SconDockerIP4     = SconSubnet4 + ".2"
	SconHostBridgeIP4 = SconSubnet4 + ".3"

	SconSubnet6       = "fd07:b51a:cc66:0000:"
	SconSubnet6CIDR   = SconSubnet6 + ":/64"
	SconGatewayIP6    = SconSubnet6 + ":1"
	SconDockerIP6     = SconSubnet6 + ":2"
	SconHostBridgeIP6 = SconSubnet6 + ":3"
)

// static ARP/neighbors to save CPU
const (
	GuestMACPrefix = "86:6c:f1:2e:9e"
	GuestMACVnet   = GuestMACPrefix + ":01"

	GatewayMAC = "24:d2:f4:58:34:d7"
)
