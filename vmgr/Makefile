.PHONY: run scli app serve publish vmgr

vmgr: ../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\)

run: vmgr
	@orb stop || :
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

run-raw: vmgr
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

# dev == debug
export RUST_BUILD_TYPE ?= release-with-debug
export RUST_BACKTRACE ?= full
export RUST_LOG ?= utils::span=info

../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\):
	@cd ../swift; make lib-debug
	@[[ ! -d ../vendor/libkrun ]] && git clone git@github.com:orbstack/libkrun-macvirt ../vendor/libkrun || :
	@cd ../vendor/libkrun; [[ "$(RUST_BUILD_TYPE)" == "debug" ]] && cargo build || cargo build --profile "$(RUST_BUILD_TYPE)"
	@./build.sh

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
