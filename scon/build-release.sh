#!/usr/bin/env bash

out="${1:-.}"

# must be static
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -tags release -o $out ./cmd/scon-agent
go build -trimpath -ldflags="-s -w" -tags release -o $out
