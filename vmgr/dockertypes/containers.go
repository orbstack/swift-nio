package dockertypes

import "time"

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
	NetworkSettings *SummaryNetworkSettings
	Mounts          []MountPoint
}

// minimized version:
// the more fields we try to decode, the greater the chance of failure
type ContainerSummaryMin struct {
	ID     string `json:"Id"`
	Mounts []MountPoint
}

// Identifiable - for scon agent
func (c ContainerSummaryMin) Identifier() string {
	return c.ID
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

type SummaryNetworkSettings struct {
	Networks map[string]*NetworkEndpointSettings
}

type EndpointIPAMConfig struct {
	IPv4Address  string   `json:",omitempty"`
	IPv6Address  string   `json:",omitempty"`
	LinkLocalIPs []string `json:",omitempty"`
}

type NetworkEndpointSettings struct {
	// Configurations
	IPAMConfig *EndpointIPAMConfig
	Links      []string
	Aliases    []string
	// Operational data
	NetworkID           string
	EndpointID          string
	Gateway             string
	IPAddress           string
	IPPrefixLen         int
	IPv6Gateway         string
	GlobalIPv6Address   string
	GlobalIPv6PrefixLen int
	MacAddress          string
	DriverOpts          map[string]string
}

type ContainerCreateRequest struct {
	Image        string
	Cmd          []string
	ExposedPorts map[string]struct{}
	HostConfig   *ContainerHostConfig
}

type ContainerCreateResponse struct {
	ID       string `json:"Id"`
	Warnings []string
}

type ContainerHostConfig struct {
	Privileged   bool
	AutoRemove   bool
	NetworkMode  string
	PortBindings map[string][]PortBinding
	Binds        []string
}

type PortBinding struct {
	HostIP   string
	HostPort string
}

type HealthcheckResult struct {
	Start    time.Time // Start is the time this check started
	End      time.Time // End is the time this check ended
	ExitCode int       // ExitCode meanings: 0=healthy, 1=unhealthy, 2=reserved (considered unhealthy), else=error running probe
	Output   string    // Output from last check
}

type Health struct {
	Status        string               // Status is one of Starting, Healthy or Unhealthy
	FailingStreak int                  // FailingStreak is the number of consecutive failures
	Log           []*HealthcheckResult // Log contains the last few results (oldest first)
}

type HealthConfig struct {
	// Test is the test to perform to check that the container is healthy.
	// An empty slice means to inherit the default.
	// The options are:
	// {} : inherit healthcheck
	// {"NONE"} : disable healthcheck
	// {"CMD", args...} : exec arguments directly
	// {"CMD-SHELL", command} : run command with system's default shell
	Test []string `json:",omitempty"`

	// Zero means to inherit. Durations are expressed as integer nanoseconds.
	Interval    time.Duration `json:",omitempty"` // Interval is the time to wait between checks.
	Timeout     time.Duration `json:",omitempty"` // Timeout is the time to wait before considering the check to have hung.
	StartPeriod time.Duration `json:",omitempty"` // The start period for the container to initialize before the retries starts to count down.

	// Retries is the number of consecutive failures needed to consider a container as unhealthy.
	// Zero means inherit.
	Retries int `json:",omitempty"`
}

type ContainerState struct {
	Status     string // String representation of the container state. Can be one of "created", "running", "paused", "restarting", "removing", "exited", or "dead"
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	Pid        int
	ExitCode   int
	Error      string
	StartedAt  string
	FinishedAt string
	Health     *Health `json:",omitempty"`
}

type ContainerNode struct {
	ID        string
	IPAddress string `json:"IP"`
	Addr      string
	Name      string
	Cpus      int
	Memory    int64
	Labels    map[string]string
}

type GraphDriverData struct {
	// Low-level storage metadata, provided as key/value pairs.
	//
	// This information is driver-specific, and depends on the storage-driver
	// in use, and should be used for informational purposes only.
	//
	// Required: true
	Data map[string]string `json:"Data"`

	// Name of the storage driver.
	// Required: true
	Name string `json:"Name"`
}

type ContainerJSONBase struct {
	ID              string `json:"Id"`
	Created         string
	Path            string
	Args            []string
	State           *ContainerState
	Image           string
	ResolvConfPath  string
	HostnamePath    string
	HostsPath       string
	LogPath         string
	Node            *ContainerNode `json:",omitempty"` // Node is only propagated by Docker Swarm standalone API
	Name            string
	RestartCount    int
	Driver          string
	Platform        string
	MountLabel      string
	ProcessLabel    string
	AppArmorProfile string
	ExecIDs         []string
	// too complex
	HostConfig  map[string]any
	GraphDriver GraphDriverData
	SizeRw      *int64 `json:",omitempty"`
	SizeRootFs  *int64 `json:",omitempty"`
}

// string | []string
type strSlice = any
type NatPortSet map[string]struct{}

type ContainerConfig struct {
	Hostname        string              // Hostname
	Domainname      string              // Domainname
	User            string              // User that will run the command(s) inside the container, also support user:group
	AttachStdin     bool                // Attach the standard input, makes possible user interaction
	AttachStdout    bool                // Attach the standard output
	AttachStderr    bool                // Attach the standard error
	ExposedPorts    NatPortSet          `json:",omitempty"` // List of exposed ports
	Tty             bool                // Attach standard streams to a tty, including stdin if it is not closed.
	OpenStdin       bool                // Open stdin
	StdinOnce       bool                // If true, close stdin after the 1 attached client disconnects.
	Env             []string            // List of environment variable to set in the container
	Cmd             strSlice            // Command to run when starting the container
	Healthcheck     *HealthConfig       `json:",omitempty"` // Healthcheck describes how to check the container is healthy
	ArgsEscaped     bool                `json:",omitempty"` // True if command is already escaped (meaning treat as a command line) (Windows specific).
	Image           string              // Name of the image as it was passed by the operator (e.g. could be symbolic)
	Volumes         map[string]struct{} // List of volumes (mounts) used for the container
	WorkingDir      string              // Current directory (PWD) in the command will be launched
	Entrypoint      strSlice            // Entrypoint to run when starting the container
	NetworkDisabled bool                `json:",omitempty"` // Is network disabled
	MacAddress      string              `json:",omitempty"` // Mac Address of the container
	OnBuild         []string            // ONBUILD metadata that were defined on the image Dockerfile
	Labels          map[string]string   // List of labels set to this container
	StopSignal      string              `json:",omitempty"` // Signal to stop a container
	StopTimeout     *int                `json:",omitempty"` // Timeout (in seconds) to stop a container
	Shell           strSlice            `json:",omitempty"` // Shell for shell-form of RUN, CMD, ENTRYPOINT
}

type ContainerJSON struct {
	*ContainerJSONBase
	Mounts          []MountPoint
	Config          *ContainerConfig
	NetworkSettings *NetworkSettings
}

type NetworkSettings struct {
	//NetworkSettingsBase
	//DefaultNetworkSettings
	Networks map[string]*NetworkEndpointSettings
}

type NetworkNetworkingConfig struct {
	EndpointsConfig map[string]*NetworkEndpointSettings // Endpoint configs for each connecting network
}

type FullContainerCreateRequest struct {
	*ContainerConfig
	HostConfig       map[string]any
	NetworkingConfig *NetworkNetworkingConfig
}
