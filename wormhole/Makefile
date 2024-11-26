.PHONY: publish-local-debug publish-local-release publish replace

publish-dev-debug:
	@./publish.sh dev debug

publish-dev-release:
	@./publish.sh dev release

publish:
	@./publish.sh prod release

# can't replace cattach because alpine rust links dynamically
replace:
	ssh -t ovm "cd /mnt/mac/$(PWD); umount -f /opt/orb/wormholefsd; cargo build && mount --bind target/debug/wormholefsd /opt/orb/wormholefsd"
