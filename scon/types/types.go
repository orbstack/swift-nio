package types

import "time"

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
	Isolated        bool   `json:"isolated"`
	DefaultUsername string `json:"default_username"`
}

type ContainerInfo struct {
	Record   *ContainerRecord `json:"record"`
	DiskSize *uint64          `json:"disk_size"`
}

const ExportVersion = 1

type ExportedMachineV1 struct {
	Version int `json:"version"`

	Record     ContainerRecord `json:"record"`
	ExportedAt time.Time       `json:"exported_at"`

	HostUID uint32 `json:"host_uid"`
	HostGID uint32 `json:"host_gid"`

	SourceFS   string                     `json:"source_fs"`
	Subvolumes []ExportedMachineSubvolume `json:"subvolumes"`
}

type ExportedMachineSubvolume struct {
	Path         string `json:"path"`
	ParentQgroup string `json:"parent_qgroup,omitempty"`
	ReadOnly     bool   `json:"read_only,omitempty"`
}

type CreateRequest struct {
	Name  string    `json:"name"`
	Image ImageSpec `json:"image"`

	// config
	Config       MachineConfig `json:"config"`
	UserPassword string        `json:"user_password,omitempty"`

	CloudInitUserData    string `json:"cloud_init_user_data"`
	InternalUseTestCache bool   `json:"internal_use_test_cache,omitempty"`
}

type ImportContainerFromHostPathRequest struct {
	NewName  string `json:"new_name,omitempty"`
	HostPath string `json:"host_path"`
}

type GetByIDRequest struct {
	ID string `json:"id"`
}

type GetByNameRequest struct {
	Name string `json:"name"`
}

type ContainerGetLogsRequest struct {
	ContainerKey string  `json:"container"`
	Type         LogType `json:"type"`
}

type InternalReportStoppedRequest struct {
	ID string `json:"id"`
}

type InternalDockerMigrationLoadImageRequest struct {
	RemoteImageNames []string `json:"remote_image_id"`
	RemoteConnFdxSeq uint64   `json:"-"`
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

type ContainerCloneRequest struct {
	ContainerKey string `json:"container_key"`
	NewName      string `json:"new_name"`
}

type ContainerRenameRequest struct {
	ContainerKey string `json:"container_key"`
	NewName      string `json:"new_name"`
}

type ContainerSetConfigRequest struct {
	ContainerKey string        `json:"container_key"`
	Config       MachineConfig `json:"config"`
}

type ContainerExportRequest struct {
	ContainerKey string `json:"container_key"`
	HostPath     string `json:"host_path"`
}

type GenericContainerRequest struct {
	Key string `json:"key"`
}

// Swift Codable ADT format
type StatsID struct {
	CgroupPath *StatsIDCgroup `json:"cgroup_path,omitempty"`
	PID        *StatsIDPID    `json:"pid,omitempty"`
}

type StatsIDCgroup struct {
	Value string `json:"_0"`
}

type StatsIDPID struct {
	Value uint32 `json:"_0"`
}

// Swift Codable ADT format
type StatsEntity struct {
	Machine   *StatsEntityMachine   `json:"machine,omitempty"`
	Container *StatsEntityContainer `json:"container,omitempty"`
	Service   *StatsEntityService   `json:"service,omitempty"`
}

type StatsEntityMachine struct {
	ID string `json:"id"`
}

type StatsEntityContainer struct {
	ID string `json:"id"`
}

type StatsEntityService struct {
	ID string `json:"id"`
}

type StatsEntry struct {
	ID StatsID `json:"id"`

	Entity StatsEntity `json:"entity"`

	// delta-based metrics; client is responsible for diffing
	CPUUsageUsec   uint64 `json:"cpu_usage_usec"`
	DiskReadBytes  uint64 `json:"disk_read_bytes"`
	DiskWriteBytes uint64 `json:"disk_write_bytes"`

	// absolute metrics
	MemoryBytes uint64 `json:"memory_bytes"`

	Children []*StatsEntry `json:"children"`
}

type StatsRequest struct {
	IncludeProcessCgPaths []string `json:"include_process_cg_paths"`
}

type StatsResponse struct {
	Entries []*StatsEntry `json:"entries"`
}
