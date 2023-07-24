.PHONY: scli proto bpfgen run

all: proto scli

scli:
	@mkdir -p ../out
	@go build -o ../out/scli ./cmd/scli

bpf/lfwd_%: bpf/src/lfwd.c
	@go generate ./bpf

bpf/ptrack_%: bpf/src/ptrack.c
	@go generate ./bpf

bpfgen: bpf/lfwd_bpfel.o bpf/lfwd_bpfel.go bpf/ptrack_bpfel.o bpf/ptrack_bpfel.go

run:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh && ./run.sh"

run-cover:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh -cover && GOCOVERDIR=/mnt/mac/$(shell pwd)/cover ./run.sh"
