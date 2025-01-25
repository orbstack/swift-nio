#!/bin/sh

set -euf

cd "$(dirname "$0")"

# new ISA (requires kernel 6.6 + LLVM 18)
# debian has headers in /usr/include/...-linux-gnu
BPF_CFLAGS="-mcpu=v4 -I/usr/include/$(uname -m)-linux-gnu"

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" bnat src/bnat.c
# strip source line info
go run ../cmd/btfstrip bnat_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" lfwd src/lfwd.c
# strip source line info
go run ../cmd/btfstrip lfwd_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" pmon src/pmon.c
# strip source line info
go run ../cmd/btfstrip pmon_bpfel.o

go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" tproxy src/tproxy.c
# strip source line info
go run ../cmd/btfstrip tproxy_bpfel.o
