.PHONY: run scli app serve publish vmgr

run: ../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\)
	@orb shutdown -f || :
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

run-raw: ../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\)
	@"../out/OrbStack Helper.app/Contents/MacOS/OrbStack Helper" vmgr

../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\):
	@cd ../swift; make lib-debug
	@./build.sh

vmgr: ../out/OrbStack\ Helper\ \(VM\).app/Contents/MacOS/OrbStack\ Helper\ \(VM\)

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
