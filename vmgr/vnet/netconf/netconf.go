package netconf

const (
	Subnet4       = "198.19.248"
	GatewayIP4    = Subnet4 + ".1"
	GuestIP4      = Subnet4 + ".2"
	ServicesIP4   = Subnet4 + ".200"
	SecureSvcIP4  = Subnet4 + ".201"
	ExtHostNatIP4 = Subnet4 + ".253"
	HostNatIP4    = Subnet4 + ".254"

	Subnet6 = "fd07:b51a:cc66:f0:"
	// hack: because we don't implement NDP, we need to use a different subnet for anything that's not guest or gateway
	SubnetExt6    = "fd07:b51a:cc66:f1:"
	GatewayIP6    = Subnet6 + ":1"
	GuestIP6      = Subnet6 + ":2"
	ExtHostNatIP6 = SubnetExt6 + ":253"
	HostNatIP6    = SubnetExt6 + ":254"
)

const (
	SconSubnet4       = "198.19.249"
	SconSubnet4CIDR   = SconSubnet4 + ".0/24"
	SconSubnet4Mask   = "255.255.255.0"
	SconGatewayIP4    = SconSubnet4 + ".1"
	SconDockerIP4     = SconSubnet4 + ".2"
	SconHostBridgeIP4 = SconSubnet4 + ".3"
	SconWebIndexIP4   = SconGatewayIP4

	SconSubnet6       = "fd07:b51a:cc66:0:"
	SconSubnet6CIDR   = SconSubnet6 + ":/64"
	SconGatewayIP6    = SconSubnet6 + ":1"
	SconDockerIP6     = SconSubnet6 + ":2"
	SconHostBridgeIP6 = NAT64SourceIP6 // to make NAT64 easier
	SconWebIndexIP6   = SconGatewayIP6

	// must be under SconSubnet6/64 due to macOS vmnet routing (neighbors)
	// chosen to be checksum-neutral for stateless NAT64 w/o L4 (TCP/UDP) checksum update: this prefix adds up to 0
	NAT64Subnet6     = "fd07:b51a:cc66:0:a617:db5e:"
	NAT64Subnet6CIDR = NAT64Subnet6 + ":/96"
	NAT64SourceIP4   = Subnet4 + ".64" // 198.19.248.64
	// /96 prefix + /32 suffix = IPv4 198.19.248.64, mapped
	NAT64SourceIP6 = NAT64Subnet6 + "c613:f840"
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

	// dummy, not really used, just to make Linux happy
	NAT64SourceMAC = BlockMACPrefix + ":e1:03"

	// vlan router uses entire :e2 block
	// lower 7 bits: vlan id / interface index
	// upper 1 bit: 0=host 1=guest
	VlanRouterMACPrefix   = BlockMACPrefix + ":e2"
	VlanRouterMACTemplate = VlanRouterMACPrefix + ":00"
)

// Docker:

// 192.168 tends to be least used according to GitHub Code Search scraping
// here, we optimize for both the .0 and .1 /23 pair between low
// first two are flipped: for the default net, we prioritize the .0 (base) part being lower. 60% weight for first, 40% total
// our logic: 172.x will prob conflict anyway
// let's go with /24 to optimize for min conflicts in common case (<255 containers).
// users can change if needed. this is also simpler for devs
const DockerBIP = "192.168.215.1/24"

// change default addrs to minimize conflicts with other networks
var DockerDefaultAddressPools = []map[string]any{
	// custom: first 32 from script (25 - 2)
	{"base": "192.168.215.0/24", "size": 24},
	{"base": "192.168.228.0/24", "size": 24},
	// reserved for possible future machines use
	//{"base": "192.168.243.0/24", "size": 24},
	{"base": "192.168.247.0/24", "size": 24},
	{"base": "192.168.207.0/24", "size": 24},
	{"base": "192.168.167.0/24", "size": 24},
	{"base": "192.168.107.0/24", "size": 24},
	{"base": "192.168.237.0/24", "size": 24},
	{"base": "192.168.148.0/24", "size": 24},
	{"base": "192.168.214.0/24", "size": 24},
	{"base": "192.168.165.0/24", "size": 24},
	{"base": "192.168.227.0/24", "size": 24},
	{"base": "192.168.181.0/24", "size": 24},
	{"base": "192.168.158.0/24", "size": 24},
	{"base": "192.168.117.0/24", "size": 24},
	{"base": "192.168.155.0/24", "size": 24},
	{"base": "192.168.194.0/24", "size": 24},
	{"base": "192.168.147.0/24", "size": 24},
	{"base": "192.168.229.0/24", "size": 24},
	{"base": "192.168.183.0/24", "size": 24},
	{"base": "192.168.156.0/24", "size": 24},
	{"base": "192.168.97.0/24", "size": 24},
	{"base": "192.168.171.0/24", "size": 24},
	{"base": "192.168.186.0/24", "size": 24},
	// removed: < 100 even number is prob common
	//{"base": "192.168.94.0/24", "size": 24},
	{"base": "192.168.216.0/24", "size": 24},
	{"base": "192.168.242.0/24", "size": 24},
	{"base": "192.168.166.0/24", "size": 24},
	{"base": "192.168.239.0/24", "size": 24},
	{"base": "192.168.223.0/24", "size": 24},
	{"base": "192.168.164.0/24", "size": 24},
	{"base": "192.168.163.0/24", "size": 24},
	{"base": "192.168.172.0/24", "size": 24},
	{"base": "192.168.138.0/24", "size": 24},

	// Docker defaults for overflow (and compat, if explicit subnet is specified for a network)
	{"base": "172.17.0.0/16", "size": 16},
	{"base": "172.18.0.0/16", "size": 16},
	{"base": "172.19.0.0/16", "size": 16},
	{"base": "172.20.0.0/14", "size": 16},
	{"base": "172.24.0.0/14", "size": 16},
	{"base": "172.28.0.0/14", "size": 16},
	// remove the 192.168 pool to avoid conflicts
	//{"base": "192.168.0.0/16", "size": 20},
}
