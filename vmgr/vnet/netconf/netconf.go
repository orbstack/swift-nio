package netconf

const (
	VnetSubnet4       = "198.19.248"
	VnetSubnet4CIDR   = VnetSubnet4 + ".0/24"
	VnetGatewayIP4    = VnetSubnet4 + ".1"
	VnetGuestIP4      = VnetSubnet4 + ".2"
	VnetHttpProxyIP4  = VnetSubnet4 + ".199"
	VnetServicesIP4   = VnetSubnet4 + ".200"
	VnetSecureSvcIP4  = VnetSubnet4 + ".201"
	VnetExtHostNatIP4 = VnetSubnet4 + ".253"
	VnetHostNatIP4    = VnetSubnet4 + ".254"

	VnetSubnet6       = "fd07:b51a:cc66:f0:"
	VnetSubnet6CIDR   = VnetSubnet6 + ":/64"
	VnetGatewayIP6    = VnetSubnet6 + ":1"
	VnetGuestIP6      = VnetSubnet6 + ":2"
	VnetHttpProxyIP6  = VnetSubnet6 + ":c7"
	VnetExtHostNatIP6 = VnetSubnet6 + ":fd"
	VnetHostNatIP6    = VnetSubnet6 + ":fe"
)

const (
	SconSubnet4       = "198.19.249"
	SconSubnet4CIDR   = SconSubnet4 + ".0/24"
	SconSubnet4Mask   = "255.255.255.0"
	SconGatewayIP4    = SconSubnet4 + ".1"
	SconDockerIP4     = SconSubnet4 + ".2"
	SconK8sIP4        = SconDockerIP4
	SconHostBridgeIP4 = SconSubnet4 + ".3"
	SconWebIndexIP4   = SconGatewayIP4

	// :0 is canonical format, not :0000
	SconSubnet6       = "fd07:b51a:cc66:0:"
	SconSubnet6CIDR   = SconSubnet6 + ":/64"
	SconGatewayIP6    = SconSubnet6 + ":1"
	SconDockerIP6     = SconSubnet6 + ":2"
	SconDockerIP6CIDR = SconDockerIP6 + "/64"
	SconK8sIP6        = SconDockerIP6
	SconHostBridgeIP6 = NAT64SourceIP6 // to make NAT64 easier
	SconWebIndexIP6   = SconGatewayIP6

	// must be under SconSubnet6/64 due to macOS vmnet routing (neighbors)
	// chosen to be checksum-neutral for stateless NAT64 w/o L4 (TCP/UDP) checksum update: this prefix adds up to 0 in big-endian 16-bit internet checksum
	// calculated by randomly generating "a617", summing all values, and then subtracting from 0xffff for the last one
	NAT64Subnet6     = "fd07:b51a:cc66:0:a617:db5e:"
	NAT64Subnet6CIDR = NAT64Subnet6 + ":/96"
	NAT64SourceIP4   = "10.183.233.241"
	// /96 prefix + /32 suffix = IPv4 10.183.233.241, mapped
	NAT64SourceIP6 = NAT64Subnet6 + "0ab7:e9f1"
)

// static ARP/neighbors to save CPU
// all under random block U/L block:
const (
	BlockMACPrefix = "da:9b:d0:54"

	// we start at :e0
	GuestMACPrefix = BlockMACPrefix + ":e0"
	// da:9b:d0:54:e0:01
	GuestMACVnet = GuestMACPrefix + ":01"
	// da:9b:d0:54:e0:02
	GuestMACSconBridge = GuestMACPrefix + ":02"

	// gateway and vmnet use :e1 block
	// da:9b:d0:54:e1:01
	HostMACVnet = BlockMACPrefix + ":e1:01"
	// da:9b:d0:54:e1:02
	HostMACSconBridge = BlockMACPrefix + ":e1:02"

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
	// reserved for k8s
	//{"base": "192.168.194.0/24", "size": 24},
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
	// reserved for k8s
	//{"base": "192.168.138.0/24", "size": 24},

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

// default max pods is 110, so we can fit it in here
const K8sClusterCIDR4 = "192.168.194.0/25"
const K8sServiceCIDR4 = "192.168.194.128/25"
const K8sNodeCIDRMaskSize4 = "25"

// for bridging to host
// cluster and service CIDRs must be adjacent
const K8sMergedCIDR4 = "192.168.194.0/24"

// k8s uses ipv4 number to assign here, so do it conservatively to reserve space for future use
const K8sClusterCIDR6 = "fd07:b51a:cc66:a::/72"
const K8sServiceCIDR6 = "fd07:b51a:cc66:a:8000::/112"
const K8sNodeCIDRMaskSize6 = "72"
const K8sMergedCIDR6 = "fd07:b51a:cc66:a::/64" // remember: macOS can only do /64

// this is a safe assumption. check orb-coredns.yaml
// it's first services IP (.0) + 10
const K8sCorednsIP4 = "192.168.194.138"
