.PHONY: scli bpfgen run

all: scli

scli:
	@mkdir -p ../out
	@go build -o ../out/scli ./cmd/scli
	@source ../config.sh; codesign -f --timestamp --options=runtime --entitlements scli.entitlements -i dev.kdrag0n.MacVirt.scli -s "$$SIGNING_CERT_DEV" ../out/scli || :

# cat buffering improves speed a lot on virtiofs
bpf/vmlinux.h: /sys/kernel/btf/vmlinux
	@bpftool btf dump file /sys/kernel/btf/vmlinux format c | cat > bpf/vmlinux.h

bpf/bnat_%: bpf/src/bnat.c
	@go generate ./bpf

bpf/lfwd_%: bpf/src/lfwd.c bpf/src/cfwd.h
	@go generate ./bpf

bpf/pmon_%: bpf/src/pmon.c
	@go generate ./bpf

bpfgen: bpf/bnat_bpfel.o bpf/bnat_bpfel.go bpf/lfwd_bpfel.o bpf/lfwd_bpfel.go bpf/pmon_bpfel.o bpf/pmon_bpfel.go

run:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh && ./run.sh"

run-cover:
	ssh -t ovm "cd /mnt/mac/$(shell pwd); killall scon; ./build.sh -cover && GOCOVERDIR=/mnt/mac/$(shell pwd)/cover ./run.sh"
