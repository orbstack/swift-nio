#!/usr/bin/env bash

set -euf

cd "$(dirname "$0")"

# new ISA (requires kernel 6.6 + LLVM 18)
# debian has headers in /usr/include/...-linux-gnu
BPF_CFLAGS="-mcpu=v4 -I/usr/include/$(uname -m)-linux-gnu"

BPF_PROGS=(bnat lfwd pmon tproxy)

for prog in "${BPF_PROGS[@]}"; do
	go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel -cflags "$BPF_CFLAGS" $prog src/$prog.c
	# strip source line info
	go run ../cmd/btfstrip ${prog}_bpfel.o
done
