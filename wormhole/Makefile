.PHONY: replace publish

local-publish:
	cd .. && VERSION=1 docker buildx bake -f rootfs/docker-bake.hcl wormhole
	docker push registry.orb.local/wormhole:1

publish:
	@./publish.sh

# can't replace cattach because alpine rust links dynamically
replace:
	ssh -t ovm "cd /mnt/mac/$(PWD); umount -f /opt/orb/wormholefsd; cargo build && mount --bind target/debug/wormholefsd /opt/orb/wormholefsd"
