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
// all under random block U/L block:
const (
	//da9bd054-66ca-48ab-8b7a-ac47d3a2698a
	BlockMACPrefix = "da:9b:d0:54"

	// we start at :e0
	GuestMACPrefix = BlockMACPrefix + ":e0"
	GuestMACVnet   = GuestMACPrefix + ":01"

	// gateway and vmnet use :e1 block
	GatewayMAC        = BlockMACPrefix + ":e1:01"
	SconHostBridgeMAC = BlockMACPrefix + ":e1:02"

	// vlan router uses entire :e2 block
	// lower 7 bits: vlan id / interface index
	// upper 1 bit: 0=host 1=guest
	VlanRouterMACPrefix   = BlockMACPrefix + ":e2"
	VlanRouterMACTemplate = VlanRouterMACPrefix + ":00"
)
