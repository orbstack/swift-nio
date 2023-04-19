#!/usr/bin/env bash

set -euxo pipefail

make bpfgen

# must be static
CGO_ENABLED=0 go build ./cmd/scon-agent
CGO_ENABLED=0 go build ./cmd/scon-forksftp
cp -f scon-forksftp /opt/orbstack-guest/ || :

# increase musl stack size 128->200k to fix lxc fork+start overflow
# https://github.com/lxc/lxc/issues/4269
go build -ldflags '-extldflags "-Wl,-z,stack-size=0x100000"' "$@"
