package dockertypes

type UIEntity string

const (
	UIEventContainer UIEntity = "container"
	UIEventVolume    UIEntity = "volume"
	UIEventImage     UIEntity = "image"
)

type UIEvent struct {
	Changed []UIEntity `json:"changed"`
}
