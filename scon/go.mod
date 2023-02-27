module github.com/kdrag0n/macvirt/scon

go 1.19

replace github.com/kdrag0n/macvirt/macvmgr => ../macvmgr

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

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
	github.com/briandowns/spinner v1.20.0
	github.com/creachadair/jrpc2 v0.43.0
	github.com/cyphar/filepath-securejoin v0.2.3
	github.com/fatih/color v1.14.1
	github.com/fsnotify/fsnotify v1.6.0
	github.com/muja/goconfig v0.0.0-20180417074348-0a635507dddc
	github.com/oklog/ulid/v2 v2.1.0
	github.com/pkg/sftp v1.13.5
	github.com/spf13/cobra v1.6.1
	github.com/stretchr/testify v1.8.1
	go.etcd.io/bbolt v1.3.6
	golang.org/x/exp v0.0.0-20230206171751-46f607a40771
	golang.org/x/term v0.4.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/petermattis/goid v0.0.0-20180202154549-b0b1615b78e5 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sasha-s/go-deadlock v0.3.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sync v0.1.0 // indirect
)

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/vishvananda/netns v0.0.0-20211101163701-50045581ed74 // indirect
	golang.org/x/crypto v0.5.0
)
