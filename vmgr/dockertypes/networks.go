package dockertypes

type Network struct {
	Name       string
	ID         string `json:"Id"`
	Scope      string
	Driver     string
	EnableIPv6 bool
	IPAM       IPAM
}

// Identifiable - for scon agent
func (n Network) Identifier() string {
	return n.ID
}

type IPAM struct {
	Driver string
	Config []IPAMConfig
}

type IPAMConfig struct {
	Subnet  string
	Gateway string
}
