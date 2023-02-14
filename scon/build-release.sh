#!/usr/bin/env bash

out="${1:-.}"
tags="${2:-release}"

# must be static
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-agent
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out ./cmd/scon-forksftp
go build -trimpath -ldflags="-s -w" -tags "$tags" -o $out
