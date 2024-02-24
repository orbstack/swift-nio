module github.com/orbstack/macvirt/scon

go 1.21.1

toolchain go1.21.4

replace github.com/orbstack/macvirt/vmgr => ../vmgr

replace gvisor.dev/gvisor => ../vendor/gvisor

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

require (
	github.com/coreos/go-iptables v0.7.0
	github.com/creack/pty v1.1.21
	github.com/gliderlabs/ssh v0.3.5
	github.com/lxc/go-lxc v0.0.0-20230926171149-ccae595aa49e
	github.com/orbstack/macvirt/vmgr v0.0.0-00010101000000-000000000000
	github.com/sirupsen/logrus v1.9.3
	github.com/vishvananda/netlink v1.2.1-beta.2
	golang.org/x/sys v0.17.0
)

require (
	github.com/alitto/pond v1.8.3
	github.com/armon/go-radix v1.0.1-0.20221118154546-54df44f2176c
	github.com/briandowns/spinner v1.20.0
	github.com/cilium/ebpf v0.13.2
	github.com/creachadair/jrpc2 v1.1.2
	github.com/docker/docker-credential-helpers v0.8.0
	github.com/docker/libkv v0.2.1
	github.com/fatih/color v1.16.0
	github.com/flosch/pongo2/v6 v6.0.0
	github.com/getsentry/sentry-go v0.27.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/miekg/dns v1.1.58
	github.com/oklog/ulid/v2 v2.1.0
	github.com/pkg/sftp v1.13.5
	github.com/sasha-s/go-deadlock v0.3.1
	github.com/spf13/cobra v1.6.1
	github.com/stretchr/testify v1.8.2
	go.etcd.io/bbolt v1.3.8
	golang.org/x/net v0.21.0
	golang.org/x/term v0.17.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/utils v0.0.0-20240102154912-e7106e64919e
)

require (
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/creachadair/mds v0.10.4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/petermattis/goid v0.0.0-20231207134359-e60b3f734c67 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/exp v0.0.0-20240222234643-814bf88cf225 // indirect
	golang.org/x/mod v0.15.0 // indirect
	golang.org/x/sync v0.6.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/tools v0.18.0 // indirect
)

require (
	github.com/alessio/shellescape v1.4.2
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/vishvananda/netns v0.0.4 // indirect
	golang.org/x/crypto v0.19.0
)
