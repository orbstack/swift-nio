GOARCH ?= arm64

build/$(GOARCH)/libgovzf.a: $(wildcard GoVZF/**/*)
	@mkdir -p build/$(GOARCH)
	@if [ "$(GOARCH)" = "arm64" ]; then SWIFT_ARCH=arm64; else SWIFT_ARCH=x86_64; fi; \
	  swiftc -emit-library -static -target $$SWIFT_ARCH-apple-macos12.4 -import-objc-header GoVZF/GoVZF.h GoVZF/*.swift -o build/$(GOARCH)/libgovzf.a

lib: build/$(GOARCH)/libgovzf.a

.PHONY: lib
