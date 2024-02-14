.PHONY: all

all: out/arm64 out/amd64

out/arm64: download-bins.sh
	@rm -fr out/arm64
	@mkdir -p out/arm64
	@cd out && ../download-bins.sh arm64 aarch64
	@mkdir -p ../swift/build
	@rm -fr ../swift/build/bins
	@cp -rc out/arm64 ../swift/build/bins

out/amd64: download-bins.sh
	@rm -fr out/amd64
	@mkdir -p out/amd64
	@cd out && ../download-bins.sh amd64 x86_64
