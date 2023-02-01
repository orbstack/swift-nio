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
	github.com/briandowns/spinner v1.20.0
	github.com/creachadair/jrpc2 v0.43.0
	github.com/cyphar/filepath-securejoin v0.2.3
	github.com/docker/docker v20.10.23+incompatible
	github.com/fsnotify/fsnotify v1.6.0
	github.com/oklog/ulid/v2 v2.1.0
	github.com/spf13/cobra v1.6.1
	go.etcd.io/bbolt v1.3.6
	golang.org/x/term v0.4.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/Code-Hex/vz/v3 v3.0.0 // indirect
	github.com/Microsoft/go-winio v0.6.0 // indirect
	github.com/docker/distribution v2.8.1+incompatible // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/fatih/color v1.7.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/mattn/go-isatty v0.0.8 // indirect
	github.com/moby/term v0.0.0-20221205130635-1aeaba878587 // indirect
	github.com/morikuni/aec v1.0.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/net v0.5.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/tools v0.1.12 // indirect
	gotest.tools/v3 v3.4.0 // indirect
)

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/vishvananda/netns v0.0.0-20211101163701-50045581ed74 // indirect
	golang.org/x/crypto v0.5.0
)
