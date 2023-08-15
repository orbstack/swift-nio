package dockertypes

import "strings"

type ImageSummary struct {
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

func (img *FullImage) Identifier() string {
	return img.ID
}

func (img *FullImage) UserTag() string {
	if len(img.RepoTags) == 0 {
		return ""
	}

	tag := img.RepoTags[0]
	// containerd image store returns these; old docker didn't
	tag = strings.Replace(tag, "docker.io/library/", "", 1)
	tag = strings.Replace(tag, "docker.io/", "", 1)
	return tag
}
