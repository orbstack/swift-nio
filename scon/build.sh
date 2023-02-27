#!/usr/bin/env bash

set -euxo pipefail

# must be static
CGO_ENABLED=0 go build ./cmd/scon-agent
CGO_ENABLED=0 go build ./cmd/scon-forksftp
cp -f scon-forksftp /opt/orbstack-guest/ || :

go build "$@"

