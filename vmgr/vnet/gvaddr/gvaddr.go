package gvaddr

import "github.com/orbstack/macvirt/vmgr/vnet/netutil"

var (
	LoopbackGvIP4 = netutil.ParseTcpipAddress("127.0.0.1")
	LoopbackGvIP6 = netutil.ParseTcpipAddress("::1")
)
