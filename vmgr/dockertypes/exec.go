package dockertypes

type ContainerExecCreateResponse struct {
	ID string `json:"Id"`
}

type ContainerExecCreateRequest struct {
	AttachStdin  bool
	AttachStdout bool
	AttachStderr bool
	Cmd          []string
	WorkingDir   string
}

type ContainerExecStartRequest struct {
	Detach bool
}

type ContainerExecInspect struct {
	ID       string
	ExitCode int
}
