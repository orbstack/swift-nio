.PHONY: app serve pub r2

app:
	@cd rootfs; make release
	@cd bins; make -j2
	@./build-app.sh

serve:
	@cd updates; python3 -m http.server

pub:
	@./publish-update.sh

r2:
	rclone sync -P updates/pub r2:orbstack-updates
