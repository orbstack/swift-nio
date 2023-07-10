package types

const (
	ContainerIDDocker = "01GQQVF6C60000000000DOCKER"
)

type LogType string

const (
	LogRuntime LogType = "runtime"
	LogConsole LogType = "console"
)

type ImageSpec struct {
	Distro  string `json:"distro"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	Variant string `json:"variant"`
}

type ContainerRecordV1 struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Image    ImageSpec `json:"image"`
	Isolated bool      `json:"isolated"`

	Builtin  bool `json:"builtin"`
	Running  bool `json:"running"`
	Deleting bool `json:"deleting"`
}

// v2
type ContainerRecord struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Image    ImageSpec `json:"image"`
	Isolated bool      `json:"isolated"`

	Builtin bool           `json:"builtin"`
	State   ContainerState `json:"state"`
}

type CreateRequest struct {
	Name         string    `json:"name"`
	Image        ImageSpec `json:"image"`
	UserPassword *string   `json:"user_password"`
}

type GetByIDRequest struct {
	ID string `json:"id"`
}

type GetByNameRequest struct {
	Name string `json:"name"`
}

type ContainerGetLogsRequest struct {
	Container *ContainerRecord `json:"container"`
	Type      LogType          `json:"type"`
}

type InternalReportStoppedRequest struct {
	ID string `json:"id"`
}

type SetDefaultUsernameRequest struct {
	Username string `json:"username"`
}

type ContainerRenameRequest struct {
	Container *ContainerRecord `json:"container"`
	NewName   string           `json:"new_name"`
}
