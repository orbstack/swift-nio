.PHONY: app server publish pub r2

app:
	@cd rootfs; make release
	@./build-app.sh

server:
	@cd updates; python3 -m http.server

serve: server

publish:
	@./publish-update.sh

pub: publish

r2:
	rclone sync -P updates/pub r2:orbstack-updates
