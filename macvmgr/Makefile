.PHONY: vmgr run scli app server serve publish pub

run: vmgr
	@orb shutdown -f || :
	@./macvmgr vmgr

vmgr:
	@./build.sh

scli:
	@cd ../scon; make

sync:
	@scp pc:~/code/android/kvm/linux/out/arch/arm64/boot/Image ../assets/debug/arm64/kernel
	@cp ../assets/debug/arm64/kernel ../assets/release/arm64/kernel
	@scp pc:~/code/android/kvm/linux/out86/arch/x86/boot/bzImage ../assets/release/amd64/kernel



app:
	@cd ..; make app

server:
	@cd ../updates; python3 -m http.server

serve: server

publish:
	@../publish-update.sh

pub: publish
