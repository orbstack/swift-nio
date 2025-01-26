# parallelism
MAKEFLAGS := -j$(J)

all: scli

.PHONY: scli
scli: wormclient/generated/wormhole.pb.go
	@./build-scli.sh

wormclient/generated/wormhole.pb.go: ../wormhole/schema/wormhole.proto
	@go generate ./wormclient

.PHONY: run
run:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh && ./run.sh"

.PHONY: run-cover
run-cover:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh -cover && GOCOVERDIR=/mnt/mac/$(shell pwd)/cover ./run.sh"

#
# BPF
#

# new ISA (requires kernel 6.6 + LLVM 18)
# debian has headers in /usr/include/...-linux-gnu
export BPF_CFLAGS := -mcpu=v4 -I/usr/include/$(shell uname -m)-linux-gnu

# cat buffering improves speed a lot on virtiofs
bpf/gen/%/vmlinux.h: ../assets/debug/%/kernel.btf
	@mkdir -p $(shell dirname $@)
	@bpftool btf dump file $< format c | cat > $@
# prevent auto deletion
.PRECIOUS: bpf/gen/%/vmlinux.h

BPF_PROGS := bnat lfwd pmon xlsm tproxy
# exclude sk_lookup programs from verification: bpftool expects "sk_lookup", cilium/ebpf expects "sk_lookup/"
BPF_VERIFY_PROGS := bnat pmon xlsm
BPF_TARGETS := arm64 x86

bpf/%_arm64_bpfel.o: bpf/gen/arm64/vmlinux.h bpf/gen/amd64/vmlinux.h bpf/src/%.c
	(cd bpf; go run github.com/cilium/ebpf/cmd/bpf2go -go-package bpf -target arm64 -cflags "$(BPF_CFLAGS) -I./gen/arm64" $(*F) src/$(*F).c)
	go run ./cmd/btfstrip $@
bpf/%_x86_bpfel.o: bpf/gen/arm64/vmlinux.h bpf/gen/amd64/vmlinux.h bpf/src/%.c
	(cd bpf; go run github.com/cilium/ebpf/cmd/bpf2go -go-package bpf -target amd64 -cflags "$(BPF_CFLAGS) -I./gen/amd64" $(*F) src/$(*F).c)
	go run ./cmd/btfstrip $@

.PHONY: bpf
bpf: $(foreach prog, $(BPF_PROGS), $(foreach target, $(BPF_TARGETS), bpf/$(prog)_$(target)_bpfel.o))

.PHONY: bpf-clean
bpf-clean:
	rm -rf bpf/gen bpf/*_bpfel.o

.PHONY: bpf-verify_%
bpf-verify_%: bpf/$(*F)_arm64_bpfel.o
# use bash for $RANDOM
	bash -c 'sudo bpftool prog load bpf/$(*F)_arm64_bpfel.o /sys/fs/bpf/scon-verify-$$RANDOM$$RANDOM$$RANDOM'

.PHONY: bpf-verify
bpf-verify: $(foreach prog, $(BPF_VERIFY_PROGS), bpf-verify_$(prog))
