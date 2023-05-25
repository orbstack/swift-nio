.PHONY: run scli app serve publish vmgr

run: OrbStack\ Helper\ \(VM\)
	@orb shutdown -f || :
	@"./OrbStack Helper (VM)" vmgr

OrbStack\ Helper\ \(VM\):
	@cd ../swift; make lib-debug
	@./build.sh

vmgr: OrbStack\ Helper\ \(VM\)

scli:
	@cd ../scon; make

sync:
	@scp pc:~/code/android/kvm/linux/out/arch/arm64/boot/Image ../assets/debug/arm64/kernel
	@cp ../assets/debug/arm64/kernel ../assets/release/arm64/kernel
	@scp pc:~/code/android/kvm/linux/out86/arch/x86/boot/bzImage ../assets/release/amd64/kernel



app:
	@cd ..; make app

serve:
	@cd ..; make serve

publish:
	@cd ..; make publish
