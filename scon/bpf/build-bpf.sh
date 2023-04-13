#!/usr/bin/env bash

set -eufo pipefail

cd "$(dirname "$0")"
go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel lfwd src/lfwd.c
# strip source line info
go run ../cmd/btfstrip lfwd_bpfel.o
