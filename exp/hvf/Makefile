vmexit: vmexit.c vmexit.entitlements
	clang vmexit.c -framework Hypervisor -O2 -o vmexit
	codesign -s - --entitlements vmexit.entitlements -f vmexit
