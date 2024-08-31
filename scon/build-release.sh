#!/usr/bin/env bash

set -euo pipefail

out="${1:-.}"
tags="${2:-release}"

# Apple M1 is ARMv8.4 + most v8.5 extensions (SB, SSBS, CCDP, FRINT3264, SPECRESTRICT, ALTERNATIVENZCV)
# just use v8.4 for simplicity -- we mainly care about specializing for LSE atomics
export GOARM64=v8.4

# strip comments from nftables rules
sed -ie 's/^\s*#.*$//g' nft/*.conf

# must be static
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-agent
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-forksftp

# increase musl stack size 128->200k to fix lxc fork+start overflow
# https://github.com/lxc/lxc/issues/4269
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -extldflags "-Wl,-z,stack-size=0x100000"' -tags "$tags" -o $out

strip $out/scon-agent
strip $out/scon-forksftp
strip $out/scon
