module github.com/orbstack/macvirt/vmgr

go 1.21.1

replace github.com/orbstack/macvirt/scon => ../scon

// go branch
// replace gvisor.dev/gvisor => github.com/orbstack/gvisor-macvirt v0.0.0-20230519042013-f7785c29c732

// dev
replace gvisor.dev/gvisor => ../vendor/gvisor

replace github.com/gliderlabs/ssh => ../vendor/glider-ssh-macvirt

replace github.com/fsnotify/fsnotify => github.com/orbstack/fsnotify-macvirt v0.0.0-20230311084904-3a09ec342ff5

replace github.com/buildbarn/go-xdr => github.com/kdrag0n/go-xdr-macvirt v0.0.0-20230326123001-605de85becc7

replace github.com/keybase/go-keychain => github.com/orbstack/go-keychain v0.0.0-20230922005607-1d526cf2beed

require (
	github.com/creachadair/jrpc2 v1.1.2
	github.com/creack/pty v1.1.21
	github.com/gliderlabs/ssh v0.3.5
	github.com/orbstack/macvirt/scon v0.0.0-00010101000000-000000000000
	golang.org/x/sys v0.17.0
)

require github.com/mikesmitty/edkey v0.0.0-20170222072505-3356ea4e686a

require (
	github.com/buildbarn/go-xdr v0.0.0-20230105161020-895955dd8771
	github.com/fatih/color v1.16.0
	github.com/fsnotify/fsnotify v1.6.0
	github.com/getsentry/sentry-go v0.27.0
	github.com/google/uuid v1.6.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/keybase/go-keychain v0.0.0-20230523030712-b5615109f100
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.29.2
	k8s.io/apimachinery v0.29.2
	k8s.io/client-go v0.29.2
)

require (
	github.com/briandowns/spinner v1.20.0 // indirect
	github.com/creachadair/mds v0.10.4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.11.2 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-openapi/jsonpointer v0.20.2 // indirect
	github.com/go-openapi/jsonreference v0.20.4 // indirect
	github.com/go-openapi/swag v0.22.9 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/petermattis/goid v0.0.0-20231207134359-e60b3f734c67 // indirect
	github.com/sasha-s/go-deadlock v0.3.1 // indirect
	golang.org/x/oauth2 v0.17.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/protobuf v1.32.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/klog/v2 v2.120.1 // indirect
	k8s.io/kube-openapi v0.0.0-20240224005224-582cce78233b // indirect
	k8s.io/utils v0.0.0-20240102154912-e7106e64919e // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

require (
	github.com/alessio/shellescape v1.4.2
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.6.1
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sync v0.6.0 // indirect
	golang.org/x/term v0.17.0
)

require (
	github.com/miekg/dns v1.1.58
	golang.org/x/crypto v0.19.0
	golang.org/x/tools v0.18.0 // indirect
)

require (
	github.com/google/btree v1.1.2 // indirect
	github.com/sirupsen/logrus v1.9.3
	golang.org/x/mod v0.15.0
	golang.org/x/net v0.21.0
	golang.org/x/time v0.5.0 // indirect
	gvisor.dev/gvisor v0.0.0-20230918234652-8a7617aed21c
)
