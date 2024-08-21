all: vmexit spinloop

vmexit: vmexit.c hvf.entitlements
	clang vmexit.c -framework Hypervisor -O2 -o vmexit
	codesign -s - --entitlements hvf.entitlements -f vmexit

spinloop: spinloop.c hvf.entitlements
	clang spinloop.c -framework Hypervisor -O2 -o spinloop -mcpu=apple-m2
	codesign -s - --entitlements hvf.entitlements -f spinloop
