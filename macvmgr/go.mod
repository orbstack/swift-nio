module github.com/kdrag0n/macvirt/macvmgr

go 1.19

replace github.com/kdrag0n/macvirt/scon => ../scon

// deps
replace github.com/Code-Hex/vz/v3 => ../../../vm/vz

// go branch
replace gvisor.dev/gvisor => ../../../vm/gvisor

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

require (
	github.com/Code-Hex/vz/v3 v3.0.0
	github.com/creachadair/jrpc2 v0.43.0
	github.com/creack/pty v1.1.18
	github.com/gliderlabs/ssh v0.3.5
	github.com/kdrag0n/macvirt/scon v0.0.0-00010101000000-000000000000
	github.com/mdlayher/vsock v1.2.0
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	golang.org/x/sys v0.4.0
)

require github.com/mikesmitty/edkey v0.0.0-20170222072505-3356ea4e686a

require (
	github.com/gofrs/flock v0.8.1
	github.com/kevinburke/ssh_config v1.2.0
	github.com/muja/goconfig v0.0.0-20180417074348-0a635507dddc
)

require golang.org/x/exp v0.0.0-20230206171751-46f607a40771 // indirect

require (
	github.com/alessio/shellescape v1.4.1
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mdlayher/socket v0.4.0 // indirect
	github.com/spf13/cobra v1.6.1
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/term v0.4.0
)

require (
	github.com/kr/fs v0.1.0 // indirect
	github.com/miekg/dns v1.1.50
	github.com/pkg/sftp v1.13.5
	golang.org/x/crypto v0.5.0
	golang.org/x/tools v0.2.0 // indirect
)

require (
	github.com/google/btree v1.0.1 // indirect
	github.com/sirupsen/logrus v1.9.0
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/net v0.5.0
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	gvisor.dev/gvisor v0.0.0-20221220191351-8ea7ab01ea4e
)
