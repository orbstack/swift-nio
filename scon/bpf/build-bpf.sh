#!/bin/sh

set -eufo pipefail

cd "$(dirname "$0")"

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel bnat src/bnat.c
# strip source line info
go run ../cmd/btfstrip bnat_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel lfwd src/lfwd.c
# strip source line info
go run ../cmd/btfstrip lfwd_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel pmon src/pmon.c
# strip source line info
go run ../cmd/btfstrip pmon_bpfel.o
