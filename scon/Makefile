.PHONY: scli proto bpfgen

all: proto scli

scli:
	@go build ./cmd/scli

bpf/lfwd_%: bpf/src/lfwd.c
	@go generate ./bpf

bpfgen: bpf/lfwd_bpfel.o bpf/lfwd_bpfel.go
