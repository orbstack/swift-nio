package main

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/vmclient"
)

// this is in here instead of orbctl because we're the one writing ssh config
// we used to use ProxyCommand + ProxyUseFdpass but that was flaky: https://github.com/orbstack/orbstack/issues/744
// somehow, after a successful proxy dial, macOS client would close the write-side of conn, causing server to close its side, then causing client to return "Connection closed by remote host"
/*
4548	2.981981	198.19.248.1	198.19.248.2	TCP	74	20052 → 2223 [SYN] Seq=0 Win=65408 [TCP CHECKSUM INCORRECT] Len=0 MSS=65477 TSval=1229234122 TSecr=0 WS=128
4551	2.982075	198.19.248.2	198.19.248.1	TCP	74	2223 → 20052 [SYN, ACK] Seq=0 Ack=1 Win=65160 [TCP CHECKSUM INCORRECT] Len=0 MSS=1460 TSval=2959818382 TSecr=1229234122 WS=128
4555	2.991400	198.19.248.1	198.19.248.2	TCP	66	20052 → 2223 [ACK] Seq=1 Ack=1 Win=65408 [TCP CHECKSUM INCORRECT] Len=0 TSval=1229234127 TSecr=2959818382
4557	2.991400	198.19.248.1	198.19.248.2	SSHv2	87	Client: Protocol (SSH-2.0-OpenSSH_9.4)
4558	2.991400	198.19.248.1	198.19.248.2	TCP	66	20052 → 2223 [FIN, ACK] Seq=22 Ack=1 Win=524288 [TCP CHECKSUM INCORRECT] Len=0 TSval=1229234127 TSecr=2959818382
4562	2.992224	198.19.248.2	198.19.248.1	SSHv2	78	Server: Protocol (SSH-2.0-Go)
4578	2.996160	198.19.248.2	198.19.248.1	SSHv2	818	Server: Key Exchange Init
4579	2.996183	198.19.248.2	198.19.248.1	TCP	66	2223 → 20052 [FIN, ACK] Seq=765 Ack=23 Win=65280 [TCP CHECKSUM INCORRECT] Len=0 TSval=2959818396 TSecr=1229234127
4589	2.998739	198.19.248.1	198.19.248.2	TCP	66	20052 → 2223 [ACK] Seq=23 Ack=766 Win=523520 [TCP CHECKSUM INCORRECT] Len=0 TSval=1229234140 TSecr=2959818392
*/
func runSshPredicate() {
	// prevent recursion from setup/scon "ssh -G" command causing deadlock
	if os.Getenv("_ORB_CALLER") != "" {
		return
	}

	// Nix build user doesn't have a $HOME
	coredir.SetOverrideHomeDir(os.Args[2])

	err := vmclient.EnsureSconVM()
	check(err)
}
