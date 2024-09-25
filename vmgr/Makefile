.PHONY: run run-swift-asan run-rust-asan run-raw scli app serve publish vmgr vmgr-swift-asan vmgr-rust-asan

# dev == debug
export RUST_BUILD_TYPE ?= release
export RUST_BACKTRACE ?= full
export RUST_LOG ?= utils::span=info

export SWIFT_BUILD_TYPE ?= debug

# workaround for Cargo .cargo/config.toml bug: https://github.com/rust-lang/cargo/issues/14350
CXXFLAGS += -mmacosx-version-min=12.3
export CXXFLAGS

vmgr:
	@cd ../swift; make lib-$(SWIFT_BUILD_TYPE)
	@cd ../virtue; [[ "$(RUST_BUILD_TYPE)" == "debug" ]] && cargo build --package krun || cargo build --package krun --profile "$(RUST_BUILD_TYPE)"
	@./build.sh

vmgr-swift-asan:
	@cd ../swift; make lib-$(SWIFT_BUILD_TYPE)-asan
	@cd ../virtue; [[ "$(RUST_BUILD_TYPE)" == "debug" ]] && cargo build --package krun || cargo build --package krun --profile "$(RUST_BUILD_TYPE)"
	@ASAN_DIR="$(wildcard $(shell xcode-select --print-path)/Toolchains/XcodeDefault.xctoolchain/usr/lib/clang/*/lib/darwin)"; EXTRA_EXTLD_FLAGS="-rpath $${ASAN_DIR} $${ASAN_DIR}/libclang_rt.asan_osx_dynamic.dylib" ./build.sh

vmgr-rust-asan:
	@cd ../swift; make lib-$(SWIFT_BUILD_TYPE)
# --target: fix proc macros being built with ASAN and loaded into rustc
	@cd ../virtue; [[ "$(RUST_BUILD_TYPE)" == "debug" ]] && RUSTFLAGS=-Zsanitizer=address cargo +nightly build -Zbuild-std --target aarch64-apple-darwin --package krun || RUSTFLAGS=-Zsanitizer=address cargo +nightly build -Zbuild-std --target aarch64-apple-darwin --package krun --profile "$(RUST_BUILD_TYPE)"
# link ASAN runtime
	@ASAN_DIR="$${HOME}/.rustup/toolchains/nightly-aarch64-apple-darwin/lib/rustlib/aarch64-apple-darwin/lib"; EXTRA_EXTLD_FLAGS="-rpath $${ASAN_DIR} $${ASAN_DIR}/librustc-nightly_rt.asan.dylib" RUST_TARGET=aarch64-apple-darwin ./build.sh

run: vmgr
	@orb stop || :
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

run-swift-asan: vmgr-swift-asan
	@orb stop || :
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

run-rust-asan: vmgr-rust-asan
	@orb stop || :
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

run-raw: vmgr
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

scli:
	@cd ../scon; make

sync:
	@scp pc:~/code/projects/orbstack/linux/out/arch/arm64/boot/Image ../assets/release/arm64/kernel
	@cp ../assets/release/arm64/kernel ../assets/debug/arm64/kernel
	@scp pc:~/code/projects/orbstack/linux/out/modules.builtin ../rootfs/kernel/arm64/modules.builtin
	@scp pc:~/code/projects/orbstack/linux/out86/arch/x86/boot/bzImage ../assets/release/amd64/kernel
	@cp ../assets/release/amd64/kernel ../assets/debug/amd64/kernel
	@scp pc:~/code/projects/orbstack/linux/out86/modules.builtin ../rootfs/kernel/amd64/modules.builtin



app:
	@cd ..; make app

serve:
	@cd ..; make serve

publish:
	@cd ..; make publish
