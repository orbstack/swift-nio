package dockertypes

type MountType string

const (
	MountTypeBind      MountType = "bind"
	MountTypeVolume    MountType = "volume"
	MountTypeTmpfs     MountType = "tmpfs"
	MountTypeNamedPipe MountType = "npipe"
	MountTypeCluster   MountType = "cluster"
)

type MountPropagation string

const (
	// PropagationRPrivate RPRIVATE
	MountPropagationRPrivate MountPropagation = "rprivate"
	// PropagationPrivate PRIVATE
	MountPropagationPrivate MountPropagation = "private"
	// PropagationRShared RSHARED
	MountPropagationRShared MountPropagation = "rshared"
	// PropagationShared SHARED
	MountPropagationShared MountPropagation = "shared"
	// PropagationRSlave RSLAVE
	MountPropagationRSlave MountPropagation = "rslave"
	// PropagationSlave SLAVE
	MountPropagationSlave MountPropagation = "slave"
)

type ContainerSummary struct {
	ID         string `json:"Id"`
	Names      []string
	Image      string
	ImageID    string
	Command    string
	Created    int64
	Ports      []Port
	SizeRw     int64 `json:",omitempty"`
	SizeRootFs int64 `json:",omitempty"`
	Labels     map[string]string
	State      string
	Status     string
	HostConfig struct {
		NetworkMode string `json:",omitempty"`
	}
	//NetworkSettings *SummaryNetworkSettings
	Mounts []MountPoint
}

type ContainerDetails struct {
	ID     string `json:"Id"`
	Mounts []MountPoint
}

type Port struct {
	IP          string `json:",omitempty"`
	PrivatePort uint16
	PublicPort  uint16 `json:",omitempty"`
	Type        string
}

type MountPoint struct {
	Type        MountType `json:",omitempty"`
	Name        string    `json:",omitempty"`
	Source      string
	Destination string
	Driver      string `json:",omitempty"`
	Mode        string
	RW          bool
	Propagation MountPropagation `json:",omitempty"`
}

type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string `json:"ID"`
		Attributes struct {
			// varies
			Container string `json:"container"`
			Name      string `json:"name"`
			Type      string `json:"type"`
		} `json:"Attributes"`
	} `json:"Actor"`
	Scope    string `json:"scope"`
	Time     int64  `json:"time"`
	TimeNano int64  `json:"timeNano"`

	// varies
	Status string `json:"status"`
	ID     string `json:"id"`
	From   string `json:"from"`
}
