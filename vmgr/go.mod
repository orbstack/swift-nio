module github.com/orbstack/macvirt/vmgr

go 1.19

replace github.com/orbstack/macvirt/scon => ../scon

// go branch
// replace gvisor.dev/gvisor => github.com/orbstack/gvisor-macvirt v0.0.0-20230519042013-f7785c29c732

// dev
replace gvisor.dev/gvisor => ../../../vm/gvisor

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

replace github.com/fsnotify/fsnotify => github.com/kdrag0n/fsnotify-macvirt v0.0.0-20230311084904-3a09ec342ff5

replace github.com/buildbarn/go-xdr => github.com/kdrag0n/go-xdr-macvirt v0.0.0-20230326123001-605de85becc7

require (
	github.com/creachadair/jrpc2 v1.0.1
	github.com/creack/pty v1.1.18
	github.com/gliderlabs/ssh v0.3.5
	github.com/orbstack/macvirt/scon v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.9.0
)

require github.com/mikesmitty/edkey v0.0.0-20170222072505-3356ea4e686a

require (
	github.com/buildbarn/go-xdr v0.0.0-20230105161020-895955dd8771
	github.com/fatih/color v1.15.0
	github.com/fsnotify/fsnotify v1.6.0
	github.com/getsentry/sentry-go v0.22.0
	github.com/google/uuid v1.3.0
	github.com/kevinburke/ssh_config v1.2.0
	github.com/muja/goconfig v0.0.0-20180417074348-0a635507dddc
	golang.org/x/exp v0.0.0-20230522175609-2e198f4a06a1
)

require (
	github.com/creachadair/mds v0.0.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/petermattis/goid v0.0.0-20230518223814-80aa455d8761 // indirect
	github.com/sasha-s/go-deadlock v0.3.1 // indirect
	golang.org/x/text v0.10.0 // indirect
	k8s.io/utils v0.0.0-20230505201702-9f6742963106 // indirect
)

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.6.1
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sync v0.3.0 // indirect
	golang.org/x/term v0.9.0
)

require (
	github.com/miekg/dns v1.1.55
	golang.org/x/crypto v0.10.0
	golang.org/x/tools v0.10.0 // indirect
)

require (
	github.com/google/btree v1.1.2 // indirect
	github.com/sirupsen/logrus v1.9.3
	golang.org/x/mod v0.11.0
	golang.org/x/net v0.11.0
	golang.org/x/time v0.3.0
	gvisor.dev/gvisor v0.0.0-20221220191351-8ea7ab01ea4e
)
