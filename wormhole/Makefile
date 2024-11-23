.PHONY: publish-local-debug publish-local-release publish replace

publish-local-debug:
	@./publish-local.sh debug

publish-local-release:
	@./publish-local.sh release

publish:
	@./publish.sh

# can't replace cattach because alpine rust links dynamically
replace:
	ssh -t ovm "cd /mnt/mac/$(PWD); umount -f /opt/orb/wormholefsd; cargo build && mount --bind target/debug/wormholefsd /opt/orb/wormholefsd"
