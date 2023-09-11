package tcpfwd

import (
	"net"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
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
	}
)

func setExtNodelay(conn *net.TCPConn, otherPort int) error {
	noDelay := false
	extPort := conn.RemoteAddr().(*net.TCPAddr).Port
	if _, ok := nodelayPorts[extPort]; ok {
		noDelay = true
	}
	if _, ok := nodelayPorts[otherPort]; ok {
		noDelay = true
	}

	err := conn.SetNoDelay(noDelay)
	if err != nil {
		return err
	}

	// take the chance to set keepalive too
	err = conn.SetKeepAlive(false)
	if err != nil {
		return err
	}

	return nil
}
