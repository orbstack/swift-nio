module github.com/orbstack/macvirt/scon

go 1.21

replace github.com/orbstack/macvirt/vmgr => ../vmgr

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

require (
	github.com/coreos/go-iptables v0.6.0
	github.com/creack/pty v1.1.18
	github.com/gliderlabs/ssh v0.3.5
	github.com/lxc/go-lxc v0.0.0-20230621012608-be98af2b8b9f
	github.com/orbstack/macvirt/vmgr v0.0.0-00010101000000-000000000000
	github.com/sirupsen/logrus v1.9.3
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/sys v0.10.0
)

require (
	github.com/alitto/pond v1.8.3
	github.com/armon/go-radix v1.0.1-0.20221118154546-54df44f2176c
	github.com/briandowns/spinner v1.20.0
	github.com/cilium/ebpf v0.10.0
	github.com/creachadair/jrpc2 v1.0.1
	github.com/docker/libkv v0.2.1
	github.com/fatih/color v1.15.0
	github.com/getsentry/sentry-go v0.23.0
	github.com/miekg/dns v1.1.55
	github.com/oklog/ulid/v2 v2.1.0
	github.com/pkg/sftp v1.13.5
	github.com/sasha-s/go-deadlock v0.3.1
	github.com/spf13/cobra v1.6.1
	github.com/stretchr/testify v1.8.2
	go.etcd.io/bbolt v1.3.7
	golang.org/x/exp v0.0.0-20230522175609-2e198f4a06a1
	golang.org/x/net v0.13.0
	golang.org/x/term v0.10.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/utils v0.0.0-20230505201702-9f6742963106
)

require (
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/creachadair/mds v0.0.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/petermattis/goid v0.0.0-20230518223814-80aa455d8761 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/sync v0.3.0 // indirect
	golang.org/x/text v0.11.0 // indirect
	golang.org/x/tools v0.11.1 // indirect
)

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	golang.org/x/crypto v0.11.0
)
