package dockertypes

type Image struct {
	ID          string `json:"Id"`
	Containers  int
	Created     int64
	Labels      map[string]string
	ParentId    string
	RepoDigests []string `json:",omitempty"`
	RepoTags    []string
	SharedSize  int64
	Size        int64
	VirtualSize int64
}

type FullImage struct {
	ID          string `json:"Id"`
	RepoTags    []string
	GraphDriver *GraphDriverData `json:",omitempty"`
	RootFS      struct {
		Type   string
		Layers []string
	}
}
