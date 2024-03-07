package main

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
)

type RuncWrapServer struct {
	unixListener net.Listener
	sconGuest    *SconGuestServer
}

func NewRuncWrapServer(sconGuest *SconGuestServer) (*RuncWrapServer, error) {
	l, err := util.ListenUnixWithPerms(mounts.DockerRuncWrapSocket, 0600, 0, 0)
	if err != nil {
		return nil, err
	}

	return &RuncWrapServer{
		unixListener: l,
		sconGuest:    sconGuest,
	}, nil
}

func (s *RuncWrapServer) Serve() error {
	for {
		conn, err := s.unixListener.Accept()
		if err != nil {
			return err
		}

		// synchronous b/c docker is the only client
		go func() {
			err := s.handleConn(conn)
			if err != nil {
				logrus.WithError(err).Error("runc stub connection failed")
			}
		}()
	}
}

func (s *RuncWrapServer) handleConn(conn net.Conn) error {
	defer conn.Close()

	// if we're starting a container, we need to synchronize container change events for NFS unmount
	// this prevents overlayfs upperdir/workdir concurrent reuse race
	// we used to do this in proxy /start endpoint, but that:
	//   - cannot be adapted to /restart, because then start AND stop are done in docker engine, before
	//     * proxy could translate /restart to /stop and /start, but that's slow and complicated
	//   - cannot be adapted to docker engine auto-restarting a crashing container, because that doesn't even go through API
	// and events are racy
	//   - even forcing a refresh at restart won't work because same container ID would be running (so not added or removed)

	// simple protocol:
	// 1. client connects and sends container ID
	// 2. server pretends container wsa removed
	// 3. server closes conn
	// 4. client execs real runc
	var buf [4]byte
	if _, err := conn.Read(buf[:]); err != nil {
		return err
	}
	cidLen := binary.LittleEndian.Uint32(buf[:])
	if cidLen != 64 /* SHA-256 */ {
		return fmt.Errorf("invalid container ID length: %d", cidLen)
	}
	cid := make([]byte, cidLen)
	n, err := conn.Read(cid)
	if err != nil {
		return err
	}
	if n != int(cidLen) {
		return fmt.Errorf("invalid container ID length: %d", n)
	}

	// pretend container was removed
	err = s.sconGuest.onDockerContainerRemovedFromCache(string(cid))
	if err != nil {
		return fmt.Errorf("on docker containers changed: %w", err)
	}

	return nil
}
