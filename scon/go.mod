module github.com/kdrag0n/macvirt/scon

go 1.19

replace github.com/kdrag0n/macvirt/macvmgr => ../macvmgr

replace github.com/gliderlabs/ssh => github.com/kdrag0n/glider-ssh-macvirt v0.0.0-20230115082436-75adbbbba209

require (
	github.com/coreos/go-iptables v0.6.0
	github.com/creack/pty v1.1.18
	github.com/gliderlabs/ssh v0.3.5
	github.com/kdrag0n/macvirt/macvmgr v0.0.0-00010101000000-000000000000
	github.com/lxc/go-lxc v0.0.0-20220627182551-ad3d9f7cb822
	github.com/sirupsen/logrus v1.9.0
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sys v0.4.0
)

require (
	github.com/oklog/ulid/v2 v2.1.0
	go.etcd.io/bbolt v1.3.6
)

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/vishvananda/netns v0.0.0-20211101163701-50045581ed74 // indirect
	golang.org/x/crypto v0.5.0 // indirect
)
