.PHONY: scli proto bpfgen run

all: proto scli

scli:
	@go build ./cmd/scli

bpf/lfwd_%: bpf/src/lfwd.c
	@go generate ./bpf

bpf/ptrack_%: bpf/src/ptrack.c
	@go generate ./bpf

bpf/sctl_%: bpf/src/sctl.c
	@go generate ./bpf

bpfgen: bpf/lfwd_bpfel.o bpf/lfwd_bpfel.go bpf/ptrack_bpfel.o bpf/ptrack_bpfel.go bpf/sctl_bpfel.o bpf/sctl_bpfel.go

run:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh && ./run.sh"
