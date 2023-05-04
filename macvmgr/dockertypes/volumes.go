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
	//UsageData  *VolumeUsageData `json:",omitempty"`
}

type VolumeListResponse struct {
	Volumes  []*Volume
	Warnings []string
}

type SystemDf struct {
	LayersSize int64
	Images     []Image
}
