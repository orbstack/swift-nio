module github.com/orbstack/macvirt/vmgr

go 1.24.3

replace github.com/orbstack/macvirt/scon => ../scon

// go branch
// replace gvisor.dev/gvisor => github.com/orbstack/gvisor-macvirt v0.0.0-20230519042013-f7785c29c732

// dev
replace gvisor.dev/gvisor => ../vendor/gvisor

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

replace github.com/fsnotify/fsnotify => github.com/orbstack/fsnotify-macvirt v0.0.0-20240424004612-788a996df377

replace github.com/keybase/go-keychain => github.com/orbstack/go-keychain v0.0.0-20230922005607-1d526cf2beed

require (
	github.com/creachadair/jrpc2 v1.3.1
	github.com/creack/pty v1.1.24
	github.com/gliderlabs/ssh v0.3.5
	github.com/orbstack/macvirt/scon v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.33.0
)

require (
	github.com/buildbarn/go-xdr v0.0.0-20240702182809-236788cf9e89
	github.com/fatih/color v1.18.0
	github.com/fsnotify/fsnotify v1.6.0
	github.com/getsentry/sentry-go v0.33.0
	github.com/google/uuid v1.6.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/keybase/go-keychain v0.0.0-20231219164618-57a3676c3af6
	github.com/sasha-s/go-deadlock v0.3.5
	go4.org/unsafe/assume-no-moving-gc v0.0.0-20231121144256-b99613f794b6
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.33.1
	k8s.io/apimachinery v0.33.1
	k8s.io/client-go v0.33.1
)

require (
	github.com/briandowns/spinner v1.20.0 // indirect
	github.com/creachadair/mds v0.24.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.12.2 // indirect
	github.com/florianl/go-nfqueue v1.3.3-0.20240511095818-c7c40990e852 // indirect
	github.com/fxamacker/cbor/v2 v2.8.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.1 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/gnostic-models v0.6.9 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/josharian/native v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mdlayher/netlink v1.7.2 // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/petermattis/goid v0.0.0-20250508124226-395b08cebbdb // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff // indirect
	k8s.io/utils v0.0.0-20250502105355-0f33e8f1c979 // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.7.0 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

require (
	github.com/alessio/shellescape v1.4.2
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.9.1
	github.com/spf13/pflag v1.0.6 // indirect
	golang.org/x/sync v0.14.0
	golang.org/x/term v0.32.0
)

require (
	github.com/miekg/dns v1.1.66
	golang.org/x/crypto v0.38.0
	golang.org/x/tools v0.33.0 // indirect
)

require (
	github.com/google/btree v1.1.3 // indirect
	github.com/kylelemons/godebug v1.1.0
	github.com/sirupsen/logrus v1.9.3
	golang.org/x/mod v0.24.0
	golang.org/x/net v0.40.0
	golang.org/x/time v0.11.0 // indirect
	gvisor.dev/gvisor v0.0.0-20230918234652-8a7617aed21c
)
