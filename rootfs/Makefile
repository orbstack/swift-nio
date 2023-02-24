.PHONY: arm64-debug amd64-debug arm64-release amd64-release release rel

arm64-debug:
	@./build.sh arm64 debug

amd64-debug:
	@./build.sh amd64 debug

arm64-release:
	@./build.sh arm64 release

amd64-release:
	@./build.sh amd64 release

release: arm64-release amd64-release

rel: release
