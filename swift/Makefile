SWIFT_ARCH ?= arm64

rwildcard=$(foreach d,$(wildcard $(1:=/*)),$(call rwildcard,$d,$2) $(filter $(subst *,%,$2),$d))

lib-debug: GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/debug/libGoVZF.a
lib-release: GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/release/libGoVZF.a

GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/release/libGoVZF.a: $(call rwildcard,GoVZF/Sources,*) GoVZF/Package.swift
	@cd GoVZF && swift build -c release --arch $(SWIFT_ARCH)
GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/debug/libGoVZF.a: $(call rwildcard,GoVZF/Sources,*) GoVZF/Package.swift
	@cd GoVZF && swift build -c debug --arch $(SWIFT_ARCH)

.PHONY: lib-debug lib-release
