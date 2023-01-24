#!/usr/bin/env bash

# must be static
CGO_ENABLED=0 go build github.com/kdrag0n/macvirt/scon/cmd/scon-agent
go build
