package tcpfwd

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type HostNFSForward struct {
	listener    net.Listener
	stack       *stack.Stack
	connectAddr tcpip.FullAddress
}

// special host TCP forward for NFS
func ListenHostNFS(s *stack.Stack, nicId tcpip.NICID, guestAddr tcpip.Address) (*HostNFSForward, int, error) {
	listener, err := netx.ListenTCP("tcp", &net.TCPAddr{
		IP: net.IPv4(127, 0, 0, 1),
		// dynamically assigned port
		Port: 0,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listen: %w", err)
	}

	f := &HostNFSForward{
		stack:    s,
		listener: listener,
		connectAddr: tcpip.FullAddress{
			NIC:  nicId,
			Addr: guestAddr,
			Port: ports.GuestNFS,
		},
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go f.handleConn(conn)
		}
	}()

	return f, listener.Addr().(*net.TCPAddr).Port, nil
}

func (f *HostNFSForward) handleConn(conn net.Conn) {
	defer conn.Close()

	// verify that it's the kernel conecting (kernel_task = pid 0), so UIDs can be trusted
	// otherwise any local process can connect to our NFS server and spoof UIDs
	// nettop is the SIP-sanctioned interface to the private network statistics API: https://newosxbook.com/bonus/vol1ch16.html
	// takes ~8 ms
	//
	// it lists matching connections with:
	// -m tcp: TCP only
	// -p 0: pid 0
	// -L 1: print CSV with 1 sample, then exit
	// -n: no reverse DNS
	// -t loopback: loopback interfaces only
	out, err := util.Run("nettop", "-m", "tcp", "-p", "0", "-L", "1", "-n", "-t", "loopback")
	if err != nil {
		logrus.WithError(err).Error("host-nfs forward: nettop failed")
		return
	}

	// check that the output contains our connection, by looking for both local and remote addresses
	// this isn't actually racy: if client closes the conn and it gets reused by kernel_task, it won't be able to talk to us anyway
	/*
		sample output:
			time,,interface,state,bytes_in,bytes_out,rx_dupe,rx_ooo,re-tx,rtt_avg,rcvsize,tx_win,tc_class,tc_mgt,cc_algo,P,C,R,W,arch,
			18:21:35.556852,kernel_task.0,,,322736,229260,0,0,0,,,,,,,,,,,,
			18:21:35.556534,tcp4 127.0.0.1:57970<->127.0.0.1:57969,lo0,Established,322736,229260,0,0,0,1.22 ms,6291456,440320,BE,-,cubic,-,-,-,-,so,
	*/
	localAddr := conn.LocalAddr().String()
	remoteAddr := conn.RemoteAddr().String()
	if !strings.Contains(out, localAddr) || !strings.Contains(out, remoteAddr) {
		logrus.WithFields(logrus.Fields{
			"local":  localAddr,
			"remote": remoteAddr,
		}).Error("host-nfs forward: blocking unauthorized connection")
		return
	} else {
		logrus.WithFields(logrus.Fields{
			"local":  localAddr,
			"remote": remoteAddr,
		}).Debug("host-nfs forward: allowing connection from kernel")
	}

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(tcpConnectTimeout))
	defer cancel()
	virtConn, err := gonet.DialContextTCP(ctx, f.stack, f.connectAddr, ipv4.ProtocolNumber)
	if err != nil {
		logrus.WithError(err).WithField("addr", f.connectAddr).Error("host-nfs forward: dial failed")
		return
	}
	defer virtConn.Close()

	pump2SpTcpGv(conn.(*net.TCPConn), virtConn)
}

func (f *HostNFSForward) Close() error {
	return f.listener.Close()
}
