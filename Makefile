include scripts/Makefile.inc

.PHONY: all
all: rootfs bins vmgr scon k8s

.PHONY: rootfs
rootfs:
	cd rootfs; make

.PHONY: bins
bins:
	cd bins; make

.PHONY: vmgr
vmgr:
	cd vmgr; make

.PHONY: scon
scon:
	cd scon; make

.PHONY: k8s
k8s: rootfs/k8s/k3s-arm64

rootfs/k8s/k3s-arm64: scripts/build-k8s.sh $(call rwildcard,vendor/k3s,*)
	scripts/build-k8s.sh

.PHONY: release
release:
	@cd rootfs; make release
	@cd bins; make
	@scripts/build-app.sh

.PHONY: rel
rel: release

.PHONY: clean
clean:
	@go clean -cache
	@rm -fr virtue/target swift/GoVZF/.build swift/DerivedData

.PHONY: serve
serve:
	@cd updates; python3 -m http.server

.PHONY: pub
pub:
	@scripts/publish-update.sh

.PHONY: r2
r2:
	# sync old builds
	cp -c updates/old/arm64/OrbStack_v1*.dmg*  updates/pub/arm64/
	cp -c updates/old/amd64/OrbStack_v1*.dmg*  updates/pub/amd64/
	rclone sync -P updates/pub --order-by modtime,ascending r2:orbstack-updates
	rclone sync -P updates/dsym --order-by modtime,ascending r2:orbstack-dsym

.PHONY: cdn
cdn:
	rclone sync -P updates/cdn --order-by modtime,ascending r2:orbstack-web
