#!/usr/bin/env bash

out="${1:-.}"

# must be static
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags release -o $out ./cmd/scon-agent
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags release -o $out ./cmd/scon-forksftp
go build -trimpath -ldflags="-s -w" -tags release -o $out
