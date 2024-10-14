#
# CONFIG
#

# 3 build types: debug (default), release, profiling
BUILD_TYPE ?= debug

# ASAN: none (default), rust, swift
ASAN ?= none

# parallel jobs
J ?= $(shell sysctl -n hw.ncpu)


#
# VARIABLES
#

# Rust and Swift have big debug/release performance differences, so use release for profiling
# Go doesn't, and has behavior changes in release, so keep it as debug
ifeq ($(BUILD_TYPE),debug)
# write all the cases out for clarity
RUST_BUILD_TYPE := debug
# cargo uses "--profile dev" for debug
CARGO_BUILD_TYPE := dev
SWIFT_BUILD_TYPE := debug
GO_BUILD_TYPE := debug
else ifeq ($(BUILD_TYPE),profiling)
RUST_BUILD_TYPE := release
CARGO_BUILD_TYPE := release
SWIFT_BUILD_TYPE := release
GO_BUILD_TYPE := debug
else ifeq ($(BUILD_TYPE),release)
RUST_BUILD_TYPE := release
CARGO_BUILD_TYPE := release
SWIFT_BUILD_TYPE := release
GO_BUILD_TYPE := release
else
$(error invalid BUILD_TYPE: $(BUILD_TYPE))
endif

# GOARCH is the main architecture knob
export GOARCH ?= arm64
# Apple M1 is ARMv8.4 + most v8.5 extensions (SB, SSBS, CCDP, FRINT3264, SPECRESTRICT, ALTERNATIVENZCV)
# just use v8.4 for simplicity -- we mainly care about specializing for LSE atomics
export GOARM64 := v8.4

# translate Go to Swift and Rust architectures
ifeq ($(GOARCH),arm64)
export SWIFT_ARCH := arm64
RUST_TARGET := aarch64-apple-darwin
else
export SWIFT_ARCH := x86_64
RUST_TARGET := x86_64-apple-darwin
endif
ifeq ($(shell uname -m),arm64)
RUST_NATIVE_TARGET := aarch64-apple-darwin
else
RUST_NATIVE_TARGET := x86_64-apple-darwin
endif
ifneq ($(RUST_TARGET),$(RUST_NATIVE_TARGET))
CARGO_FLAGS := --target $(RUST_TARGET)
endif

# for running vmgr
export RUST_BACKTRACE ?= full
export RUST_LOG ?= info

# minimumn macOS version
MACOS_VERSION_ARG := -mmacosx-version-min=13.0
# for Cgo
export CGO_CFLAGS := $(MACOS_VERSION_ARG)
export CGO_LDFLAGS := $(MACOS_VERSION_ARG)

DEVELOPER_DIR := $(shell xcode-select --print-path)

# allow replacing tools
SWIFT ?= swift
CC ?= cc
GO ?= go
CARGO ?= cargo

# ASAN
ifeq ($(ASAN),rust)
export RUSTFLAGS := -Zsanitizer=address
CARGO := $(CARGO) +nightly
# need to rebuild std with ASAN
CARGO_FLAGS := -Zbuild-std
# always specify --target: fix proc macros being built with ASAN and loaded into rustc
CARGO_FLAGS := --target $(RUST_TARGET)

# link ASAN runtime
ASAN_DIR := $(HOME)/.rustup/toolchains/nightly-$(RUST_TARGET)/lib/rustlib/$(RUST_TARGET)/lib
LDFLAGS := -rpath $(ASAN_DIR) $(ASAN_DIR)/librustc-nightly_rt.asan.dylib

else ifeq ($(ASAN),swift)
export SWIFT_FLAGS := -sanitize address
# link ASAN runtime
ASAN_DIR := $(wildcard $(DEVELOPER_DIR)/Toolchains/XcodeDefault.xctoolchain/usr/lib/clang/*/lib/darwin)
LDFLAGS := -rpath $(ASAN_DIR) $(ASAN_DIR)/libclang_rt.asan_osx_dynamic.dylib

else ifeq ($(ASAN),none)

else
$(error invalid ASAN: $(ASAN))
endif

OUT ?= ../out
ARCH_OUT := $(OUT)/$(GOARCH)

SWIFT_LIB := ../swift/GoVZF/.build/$(SWIFT_ARCH)-apple-macosx/$(SWIFT_BUILD_TYPE)/libGoVZF.a
GO_LIB := $(ARCH_OUT)/libvmgr.a
# specifying a target triple explicitly always makes a target/ subdir, even for native builds
# so only do it for non-native builds
ifeq ($(RUST_TARGET),$(RUST_NATIVE_TARGET))
RUST_LIB := ../virtue/target/$(RUST_BUILD_TYPE)/libkrun.a
else
RUST_LIB := ../virtue/target/$(RUST_TARGET)/$(RUST_BUILD_TYPE)/libkrun.a
endif

VMGR_NAME := OrbStack\ Helper
VMGR_BUNDLE := $(OUT)/$(VMGR_NAME).app
VMGR_BIN_DIR := $(VMGR_BUNDLE)/Contents/MacOS
VMGR_BIN := $(VMGR_BIN_DIR)/$(VMGR_NAME)
VMGR_DSYM := $(VMGR_BIN_DIR)/$(VMGR_NAME).dSYM

# parallelism
MAKEFLAGS := -j$(J)

# environment-related dependencies
BUILD_ENV_DEPS := .buildenv $(shell which $(SWIFT) $(CC) $(GO) $(CARGO)) $(DEVELOPER_DIR) ../config.sh ../config.local.sh


# 
# HELPERS
#

# recursive wildcard: $(call rwildcard,dir,pattern)
rwildcard=$(foreach d,$(wildcard $(1:=/*)),$(call rwildcard,$d,$2) $(filter $(subst *,%,$2),$d))


#
# TARGETS
#

.PHONY: vmgr
vmgr: $(VMGR_BIN)

# build env vars and configs
.PHONY: _always
.buildenv: _always
# our main source of overhead is "cargo version" taking ~25ms
	@echo BUILD_TYPE=$(BUILD_TYPE) ASAN=$(ASAN) CC_VERSION="$$($(CC) -v 2>&1)" GO_VERSION="$$($(GO) version)" CARGO_VERSION="$$($(CARGO) version)" > $@.tmp
	@cmp $@ $@.tmp > /dev/null || mv $@.tmp $@
	@rm -f $@.tmp

# Swift static library: GoVZF, swext
$(SWIFT_LIB): $(BUILD_ENV_DEPS) $(call rwildcard,../swift/GoVZF/Sources,*) ../swift/GoVZF/Package.swift
	cd ../swift/GoVZF; $(SWIFT) build -c $(SWIFT_BUILD_TYPE) --arch $(SWIFT_ARCH) $(SWIFT_FLAGS)
	@touch $@

# Go static library: vmgr (including main function)
$(GO_LIB): $(BUILD_ENV_DEPS) $(call rwildcard,./,*.go *.c *.h *.cpp) go.mod go.sum
# generate env-dependent Go code
	$(GO) generate ./conf/appver ./drm/killswitch

	@mkdir -p $(OUT)/$(GOARCH)
# build ./cgostub package to get the right main function
	$(GO) build -buildmode=c-archive -o $(OUT)/$(GOARCH)/libvmgr.a ./cgostub

# Rust static library: virtue
$(RUST_LIB): $(BUILD_ENV_DEPS) $(call rwildcard,../virtue/src,*) ../virtue/Cargo.toml ../virtue/Cargo.lock
# workaround for Cargo .cargo/config.toml bug: https://github.com/rust-lang/cargo/issues/14350
	cd ../virtue; CXXFLAGS=$(MACOS_VERSION_ARG) $(CARGO) build --profile $(CARGO_BUILD_TYPE) $(RUST_CARGO_TARGET) $(CARGO_FLAGS) --package krun
# .buildenv and other changes may not trigger a rebuild
# Cargo doesn't update output in that case, causing make to act like nothing changed, so it keeps reinvoking cargo
	@touch $@

# vmgr binary: Swift, Go, Rust objects
# include scripts in dependency list to rebuild on command change
# also rebuild on Go/Swift/Rust updates and Xcode changes
$(VMGR_BIN): $(BUILD_ENV_DEPS) $(SWIFT_LIB) $(GO_LIB) $(RUST_LIB) Makefile sign.sh
	@mkdir -p $(VMGR_BIN_DIR)

# link.
# cc takes care of some details; raw ld is hard to use
# search paths are necessary for Swift linking
# -no_adhoc_codesign skips redundant codesign step; we resign it later
	$(CC) \
	-arch $(SWIFT_ARCH) \
	$(MACOS_VERSION_ARG) \
	-Wl,-no_adhoc_codesign \
	-framework Hypervisor \
	-L"$(DEVELOPER_DIR)/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/swift" \
	-L"$(DEVELOPER_DIR)/Toolchains/XcodeDefault.xctoolchain/usr/lib/swift/macosx" \
	$(SWIFT_LIB) \
	$(GO_LIB) \
	$(RUST_LIB) \
	-o $(VMGR_BIN)

# depends on linked executable, must run before strip
	dsymutil $(VMGR_BIN) --linker parallel --num-threads $(J) -o $(VMGR_DSYM)

	GO_BUILD_TYPE=$(GO_BUILD_TYPE) VMGR_BUNDLE=$(VMGR_BUNDLE) VMGR_BIN=$(VMGR_BIN) ./sign.sh


#
# COMMANDS
#

.PHONY: clean
clean:
	rm -rf $(OUT) ../swift/GoVZF/.build ../virtue/target .buildenv .buildenv.tmp
	$(GO) clean -cache

.PHONY: run
run: vmgr
	orb stop || :
	$(VMGR_BIN) vmgr

.PHONY: scli
scli:
	cd ../scon; make



.PHONY: app
app:
	@cd ..; make app

.PHONY: serve
serve:
	@cd ..; make serve

.PHONY: publish
publish:
	@cd ..; make publish
