#!/bin/sh

set -euxo pipefail

# Apple M1 is ARMv8.4 + most v8.5 extensions (SB, SSBS, CCDP, FRINT3264, SPECRESTRICT, ALTERNATIVENZCV)
# just use v8.4 for simplicity -- we mainly care about specializing for LSE atomics
export GOARM64=v8.4

make bpfgen

# must be static
CGO_ENABLED=0 go build ./cmd/scon-agent
CGO_ENABLED=0 go build ./cmd/scon-forksftp
cp -f scon-forksftp /opt/orbstack-guest/ || :

# increase musl stack size 128->200k to fix lxc fork+start overflow
# https://github.com/lxc/lxc/issues/4269
go build -ldflags '-extldflags "-Wl,-z,stack-size=0x100000"' "$@"
