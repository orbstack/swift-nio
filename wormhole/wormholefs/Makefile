.PHONY: replace

# can't replace cattach because alpine rust links dynamically
replace:
	ssh -t ovm "cd /mnt/mac/$(PWD); umount -f /opt/orb/wormholefsd; cargo build && mount --bind target/debug/wormholefsd /opt/orb/wormholefsd"
