package tcpfwd

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
)

var (
	// TODO agent
	nodelayPorts = map[int]struct{}{
		// ext
		22:    {}, // SSH
		2222:  {}, // SSH
		25565: {}, // Minecraft

		// internal use
		ports.GuestDocker:   {}, // Docker
		ports.GuestNFS:      {}, // NFS
		ports.GuestVcontrol: {}, // vcontrol
		ports.HostHcontrol:  {}, // hcontrol
		ports.HostNFS:       {}, // NFS
	}
)

func setExtNodelay(conn *net.TCPConn, otherPort int) error {
	// take the chance to set keepalive too
	err := conn.SetKeepAlive(false)
	if err != nil {
		return err
	}

	err = conn.SetNoDelay(false)
	if err != nil {
		return err
	}

	extPort := conn.RemoteAddr().(*net.TCPAddr).Port
	if _, ok := nodelayPorts[extPort]; ok {
		return conn.SetNoDelay(true)
	}
	if _, ok := nodelayPorts[otherPort]; ok {
		return conn.SetNoDelay(true)
	}
	return nil
}
