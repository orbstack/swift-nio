.PHONY: arm64-debug amd64-debug arm64-release amd64-release release rel

OVERRIDE_ARCHS ?= arm64 amd64

arm64-debug:
	@./build.sh arm64 debug

amd64-debug:
	@./build.sh amd64 debug

arm64-release:
	@./build.sh arm64 release

amd64-release:
	@./build.sh amd64 release

release: $(foreach arch,$(OVERRIDE_ARCHS),$(arch)-release)

rel: release
