#!/usr/bin/env bash

out="${1:-.}"
tags="${2:-release}"

# must be static
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-agent
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-forksftp

# increase musl stack size 128->200k to fix lxc fork+start overflow
# https://github.com/lxc/lxc/issues/4269
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -extldflags "-Wl,-z,stack-size=0x100000"' -tags "$tags" -o $out
