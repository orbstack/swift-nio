package dockertypes

type Network struct {
	Name       string
	ID         string `json:"Id,omitempty"`
	Created    string `json:",omitempty"`
	Scope      string `json:",omitempty"`
	Driver     string
	EnableIPv6 bool
	IPAM       IPAM

	Internal   bool
	Attachable bool
	Ingress    bool
	ConfigFrom ConfigReference
	ConfigOnly bool
	Containers map[string]ContainerEndpoint `json:",omitempty"`
	Options    map[string]string
	Labels     map[string]string

	// create only
	CheckDuplicate bool
}

// Identifiable - for scon agent
func (n Network) Identifier() string {
	return n.ID
}

type IPAM struct {
	Driver  string
	Options map[string]any // ?
	Config  []IPAMConfig
}

type IPAMConfig struct {
	Subnet  string
	Gateway string
}

type ConfigReference struct {
	Network string
}

type ContainerEndpoint struct {
	Name        string
	EndpointID  string
	MacAddress  string
	IPv4Address string
	IPv6Address string
}

type NetworkCreateResponse struct {
	ID      string `json:"Id"`
	Warning string
}
