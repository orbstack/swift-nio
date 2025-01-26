.PHONY: scli bpfgen run

all: scli

scli: wormclient/generated/wormhole.pb.go
	@./build-scli.sh

wormclient/generated/wormhole.pb.go: ../wormhole/schema/wormhole.proto
	@go generate ./wormclient

# cat buffering improves speed a lot on virtiofs
bpf/vmlinux.h: /sys/kernel/btf/vmlinux
	@bpftool btf dump file /sys/kernel/btf/vmlinux format c | cat > bpf/vmlinux.h

BPF_PROGS := bnat lfwd pmon tproxy

bpf/%_bpfel.o: bpf/src/%.c
	@go generate ./bpf

bpfgen: $(foreach prog, $(BPF_PROGS), bpf/$(prog)_bpfel.o)

run:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh && ./run.sh"

run-cover:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh -cover && GOCOVERDIR=/mnt/mac/$(shell pwd)/cover ./run.sh"
