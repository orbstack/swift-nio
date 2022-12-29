module github.com/kdrag0n/macvirt/macvmgr

go 1.19

replace github.com/Code-Hex/vz/v3 => /Users/dragon/code/vm/vz

// go branch
replace gvisor.dev/gvisor => /Users/dragon/code/vm/gvisor

require (
	github.com/Code-Hex/vz/v3 v3.0.0
	github.com/pkg/term v1.1.0
	golang.org/x/sys v0.3.0
)

require (
	github.com/kr/fs v0.1.0 // indirect
	github.com/miekg/dns v1.1.50
	github.com/pkg/sftp v1.13.5
	github.com/stretchr/testify v1.8.1 // indirect
	golang.org/x/crypto v0.1.0 // indirect
	golang.org/x/tools v0.1.12 // indirect
)

require (
	github.com/google/btree v1.0.1 // indirect
	github.com/sirupsen/logrus v1.9.0
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/net v0.2.0
	golang.org/x/time v0.0.0-20200630173020-3af7569d3a1e // indirect
	gvisor.dev/gvisor v0.0.0-20221220191351-8ea7ab01ea4e
)
