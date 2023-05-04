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
