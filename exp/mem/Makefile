membench: membench.c membench.entitlements
	clang membench.c -framework Hypervisor -O2 -o membench
	codesign -s - --entitlements membench.entitlements -f membench

membench_linux: membench_linux.c memcpy.S memcpy-advsimd.S memcpy-advsimd-nt.S memcpy-apple.S memcpy-oryon.S memcpy-orb.S
	gcc membench_linux.c memcpy.S memcpy-advsimd.S memcpy-advsimd-nt.S memcpy-apple.S memcpy-oryon.S memcpy-orb.S -O2 -o membench_linux
