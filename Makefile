.PHONY: app serve pub r2 clean

app:
	@cd rootfs; make release
	@cd bins; make
	@scripts/build-app.sh

clean:
	@go clean -cache
	@rm -fr virtue/target swift/GoVZF/.build swift/DerivedData

serve:
	@cd updates; python3 -m http.server

pub:
	@scripts/publish-update.sh

r2:
	# sync old builds
	cp -c updates/old/arm64/OrbStack_v1*.dmg*  updates/pub/arm64/
	cp -c updates/old/amd64/OrbStack_v1*.dmg*  updates/pub/amd64/
	rclone sync -P updates/pub --order-by modtime,ascending r2:orbstack-updates
	rclone sync -P updates/dsym --order-by modtime,ascending r2:orbstack-dsym

cdn:
	rclone sync -P updates/cdn --order-by modtime,ascending r2:orbstack-web
