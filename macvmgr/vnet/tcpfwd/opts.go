package tcpfwd

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
)

var (
	// TODO agent
	nodelayPorts = map[int]struct{}{
		// ext
		22:    {}, // SSH
		2222:  {}, // SSH
		25565: {}, // Minecraft

		// internal use
		conf.GuestPortDocker:   {}, // Docker
		conf.GuestPortNFS:      {}, // NFS
		conf.GuestPortVcontrol: {}, // vcontrol
		conf.HostPortHcontrol:  {}, // hcontrol
		conf.HostPortNFS:       {}, // NFS
	}
)

func setExtNodelay(conn *net.TCPConn, otherPort int) error {
	// take the chance to set keepalive too
	err := conn.SetKeepAlive(false)
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
