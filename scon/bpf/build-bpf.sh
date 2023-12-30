#!/bin/sh

set -eufo pipefail

cd "$(dirname "$0")"

# new ISA, kernel 5.1+ (v4 requires 6.6+ and LLVM 18)
BPF_CFLAGS="-mcpu=v3"

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" bnat src/bnat.c
# strip source line info
go run ../cmd/btfstrip bnat_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" lfwd src/lfwd.c
# strip source line info
go run ../cmd/btfstrip lfwd_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" pmon src/pmon.c
# strip source line info
go run ../cmd/btfstrip pmon_bpfel.o
