package dockertypes

type UIEntity int

const (
	UIEventContainer UIEntity = iota
	UIEventVolume
	UIEventImage

	UIEventMax_
)

type UIEvent struct {
	CurrentContainers []*ContainerSummary `json:"currentContainers"`
	CurrentVolumes    []*Volume           `json:"currentVolumes"`
	CurrentImages     []*ImageSummary     `json:"currentImages"`
	CurrentSystemDf   *SystemDf           `json:"currentSystemDf"`

	Stopped bool `json:"stopped"`
}
