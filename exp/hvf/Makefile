all: vmexit vmexit_mmio vmfault spinloop

vmexit: vmexit.c hvf.entitlements
	clang vmexit.c -framework Hypervisor -O2 -o vmexit
	codesign -s - --entitlements hvf.entitlements -f vmexit

vmexit_mmio: vmexit_mmio.c hvf.entitlements
	clang vmexit_mmio.c -framework Hypervisor -O2 -o vmexit_mmio
	codesign -s - --entitlements hvf.entitlements -f vmexit_mmio

vmfault: vmfault.c hvf.entitlements
	clang vmfault.c -framework Hypervisor -O2 -o vmfault
	codesign -s - --entitlements hvf.entitlements -f vmfault

spinloop: spinloop.c hvf.entitlements
	clang spinloop.c -framework Hypervisor -O2 -o spinloop -mcpu=apple-m2
	codesign -s - --entitlements hvf.entitlements -f spinloop
