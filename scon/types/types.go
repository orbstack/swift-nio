package types

const (
	// sentinel
	DockerMigrationSyncDirImageLoad = "//..__orb_migrate_docker_image_load__"

	ContainerNameK8s = "k8s"
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
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Image ImageSpec `json:"image"`

	Config MachineConfig `json:"config"`

	Builtin bool           `json:"builtin"`
	State   ContainerState `json:"state"`
}

type MachineConfig struct {
	Isolated bool `json:"isolated"`
}

type CreateRequest struct {
	Name         string    `json:"name"`
	Image        ImageSpec `json:"image"`
	UserPassword string    `json:"user_password,omitempty"`

	CloudInitUserData string `json:"cloud_init_user_data"`
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

type InternalDockerMigrationLoadImageRequest struct {
	RemoteImageNames []string `json:"remote_image_id"`
}

type InternalDockerMigrationRunSyncServerRequest struct {
	Port int `json:"port"`
}

type InternalDockerMigrationWaitSyncRequest struct {
	JobID uint64 `json:"job_id"`
}

type InternalDockerMigrationSyncDirsRequest struct {
	JobID uint64   `json:"job_id"`
	Dirs  []string `json:"dirs"`
}

type SetDefaultUsernameRequest struct {
	Username string `json:"username"`
}

type ContainerRenameRequest struct {
	Container *ContainerRecord `json:"container"`
	NewName   string           `json:"new_name"`
}
