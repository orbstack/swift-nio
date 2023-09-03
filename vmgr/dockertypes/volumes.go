package dockertypes

type VolumeCreateOptions struct {
	Name       string            `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
	Driver     string            `json:",omitempty"`
	DriverOpts map[string]string `json:",omitempty"`
	//ClusterVolumeSpec *ClusterVolumeSpec `json:",omitempty"`
}

type Volume struct {
	//ClusterVolume *ClusterVolume `json:",omitempty"`
	CreatedAt  string `json:",omitempty"`
	Driver     string
	Labels     map[string]string
	Mountpoint string
	Name       string
	Options    map[string]string
	Scope      string
	Status     map[string]interface{} `json:",omitempty"`
	UsageData  *VolumeUsageData       `json:",omitempty"`
}

func (v Volume) Identifier() string {
	return v.Name
}

type VolumeUsageData struct {
	RefCount int
	Size     int64
}

type VolumeListResponse struct {
	Volumes  []*Volume
	Warnings []string
}

type SystemDf struct {
	LayersSize int64
	Images     []ImageSummary `json:",omitempty"`
	//Layers
	//Containers, etc
	Volumes []Volume
}

type VolumeCreateRequest struct {
	Name       string
	DriverOpts map[string]string `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
}
