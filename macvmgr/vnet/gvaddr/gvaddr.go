package gvaddr

import "github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"

var (
	LoopbackGvIP4 = netutil.ParseTcpipAddress("127.0.0.1")
	LoopbackGvIP6 = netutil.ParseTcpipAddress("::1")
)
