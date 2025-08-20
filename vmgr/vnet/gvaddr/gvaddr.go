package gvaddr

import "github.com/orbstack/macvirt/vmgr/vnet/gvnetutil"

var (
	LoopbackGvIP4 = gvnetutil.ParseTcpipAddress("127.0.0.1")
	LoopbackGvIP6 = gvnetutil.ParseTcpipAddress("::1")
)
