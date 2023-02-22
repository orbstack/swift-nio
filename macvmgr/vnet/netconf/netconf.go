package netconf

const (
	Subnet4      = "172.30.30"
	GatewayIP4   = Subnet4 + ".1"
	GuestIP4     = Subnet4 + ".2"
	ServicesIP4  = Subnet4 + ".200"
	SecureSvcIP4 = Subnet4 + ".201"
	HostNatIP4   = Subnet4 + ".254"

	Subnet6 = "fc00:96dc:7096:1d21:"
	// hack: because we don't implement NDP, we need to use a different subnet for anything that's not guest or gateway
	SubnetExt6 = "fc00:96dc:7096:1d22:"
	GatewayIP6 = Subnet6 + ":1"
	GuestIP6   = Subnet6 + ":2"
	HostNatIP6 = SubnetExt6 + ":254"
)
