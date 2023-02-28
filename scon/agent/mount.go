package agent

import (
	"os"

	"golang.org/x/sys/unix"
)

type BindMountArgs struct {
	Source string
	Target string
}

func (a *AgentServer) BindMountNfsRoot(args BindMountArgs, reply *None) error {
	// rbind, rshared, ro
	if err := unix.Mount(args.Source, args.Target, "", unix.MS_BIND|unix.MS_REC|unix.MS_SHARED|unix.MS_RDONLY, ""); err != nil {
		return err
	}

	return nil
}

func (a *AgentServer) RemoveFile(path string, reply *None) error {
	return os.Remove(path)
}
