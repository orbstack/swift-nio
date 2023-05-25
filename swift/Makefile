SWIFT_ARCH ?= arm64

rwildcard=$(foreach d,$(wildcard $(1:=/*)),$(call rwildcard,$d,$2) $(filter $(subst *,%,$2),$d))

lib-debug: GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/debug/libgovzf.a
lib-release: GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/release/libgovzf.a

GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/release/libgovzf.a: $(call rwildcard,GoVZF,*)
	@cd GoVZF && swift build -c release --arch $(SWIFT_ARCH)
GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/debug/libgovzf.a: $(call rwildcard,GoVZF,*)
	@cd GoVZF && swift build -c debug --arch $(SWIFT_ARCH)

.PHONY: lib-debug lib-release
